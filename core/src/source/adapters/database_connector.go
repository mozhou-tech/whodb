/*
 * Copyright 2026 Clidey, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package adapters exposes source connectors backed by the existing database
// plugin layer.
package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/clidey/whodb/core/src"
	"github.com/clidey/whodb/core/src/common/metadatacache"
	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/importer"
	"github.com/clidey/whodb/core/src/log"
	"github.com/clidey/whodb/core/src/plugins"
	"github.com/clidey/whodb/core/src/query"
	"github.com/clidey/whodb/core/src/querysuggestions"
	"github.com/clidey/whodb/core/src/source"
	"github.com/clidey/whodb/core/src/sourcecatalog"
)

const (
	metadataSoftTTL = 10 * time.Minute
	metadataHardTTL = 7 * 24 * time.Hour
)

func init() {
	source.RegisterDriver("database", &DatabaseConnector{})
}

// DatabaseConnector opens source sessions backed by the existing database plugin layer.
type DatabaseConnector struct{}

// EngineCredentials converts source-first credentials into the legacy engine
// credential shape required by the current database plugins.
func EngineCredentials(spec source.TypeSpec, credentials *source.Credentials) *engine.Credentials {
	session := &DatabaseSession{
		spec:        spec,
		credentials: credentials,
	}
	return session.engineCredentials(nil)
}

// Open creates a new database-backed source session.
func (c *DatabaseConnector) Open(_ context.Context, spec source.TypeSpec, credentials *source.Credentials) (source.SourceSession, error) {
	plugin := src.MainEngine.Choose(engine.DatabaseType(spec.Connector))
	if plugin == nil {
		return nil, fmt.Errorf("unsupported source connector: %s", spec.Connector)
	}

	var store *metadatacache.Store
	if flag.Lookup("test.v") == nil {
		store = metadatacache.Default()
	}

	return &DatabaseSession{
		spec:        spec,
		plugin:      plugin,
		credentials: credentials,
		validated:   map[databaseObjectCacheKey]struct{}{},
		columns:     map[databaseObjectCacheKey][]source.Column{},
		store:       store,
	}, nil
}

// Invalidate clears cached plugin connection state for one database-backed
// source credential set.
func (c *DatabaseConnector) Invalidate(_ context.Context, spec source.TypeSpec, credentials *source.Credentials) error {
	plugins.RemoveConnection(engine.NewPluginConfig(EngineCredentials(spec, credentials)))
	if flag.Lookup("test.v") == nil {
		s := &DatabaseSession{spec: spec, credentials: credentials, store: metadatacache.Default()}
		s.invalidatePersistentCache()
	}
	return nil
}

// Shutdown releases all cached plugin connections owned by the database-backed
// source driver.
func (c *DatabaseConnector) Shutdown(ctx context.Context) error {
	plugins.CloseAllConnections(ctx)
	return nil
}

// DatabaseSession adapts one database plugin instance to the source session interfaces.
type DatabaseSession struct {
	spec        source.TypeSpec
	plugin      *engine.Plugin
	credentials *source.Credentials
	mu          sync.RWMutex
	validated   map[databaseObjectCacheKey]struct{}
	columns     map[databaseObjectCacheKey][]source.Column
	store       *metadatacache.Store
}

type databaseObjectCacheKey struct {
	namespace string
	name      string
}

type sourceQueryStreamWriterAdapter struct {
	writer source.QueryStreamWriter
}

func (w *sourceQueryStreamWriterAdapter) WriteColumns(columns []engine.Column) error {
	return w.writer.WriteColumns(columns)
}

func (w *sourceQueryStreamWriterAdapter) WriteRow(row []string) error {
	return w.writer.WriteRow(row)
}

// Metadata returns source session metadata derived from the source registry.
func (s *DatabaseSession) Metadata(_ context.Context) (*source.SessionMetadata, error) {
	metadata, _ := sourcecatalog.ResolveSessionMetadata(s.spec.ID, s.spec.Connector)
	if metadata == nil {
		metadata = &source.TypeSessionMetadata{}
	}

	return &source.SessionMetadata{
		SourceType:      s.spec.ID,
		QueryLanguages:  queryLanguagesForSpec(s.spec),
		TypeDefinitions: slices.Clone(metadata.TypeDefinitions),
		Operators:       slices.Clone(metadata.Operators),
		AliasMap:        cloneAliasMap(metadata.AliasMap),
	}, nil
}

// ConnectionFieldOptions returns dynamic options for a connection field.
func (s *DatabaseSession) ConnectionFieldOptions(ctx context.Context, fieldKey string, values map[string]string) ([]string, error) {
	if !strings.EqualFold(fieldKey, "Database") {
		return []string{}, nil
	}

	scope := s.baseCacheScope()
	cacheKey := "field_options:Database"
	if s.store != nil && len(values) == 0 {
		var cached []string
		if hit, stale := s.store.Get(scope, cacheKey, &cached); hit {
			if stale {
				s.store.TriggerRefresh(scope, cacheKey, func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cfg := engine.NewPluginConfig(s.engineCredentials(nil))
					cfg.Context = bgCtx
					if fresh, err := s.plugin.GetDatabases(cfg); err == nil {
						s.store.Set(scope, cacheKey, fresh, metadataSoftTTL, metadataHardTTL)
					}
				})
			}
			return cached, nil
		}
	}

	config := engine.NewPluginConfig(s.engineCredentials(values))
	config.Context = ctx
	databases, err := s.plugin.GetDatabases(config)
	if err != nil {
		return nil, err
	}
	if s.store != nil && len(values) == 0 {
		s.store.Set(scope, cacheKey, databases, metadataSoftTTL, metadataHardTTL)
	}
	return databases, nil
}

// ListObjects lists child objects beneath the provided parent.
func (s *DatabaseSession) ListObjects(ctx context.Context, parent *source.ObjectRef, kinds []source.ObjectKind) ([]source.Object, error) {
	nextKind, ok := s.nextKind(parent)
	if !ok {
		return []source.Object{}, nil
	}
	if len(kinds) > 0 && !slices.Contains(kinds, nextKind) {
		return []source.Object{}, nil
	}
	if err := s.ensureBrowseSupported(parent); err != nil {
		return nil, err
	}

	scope := s.baseCacheScope()
	cacheKey := s.listObjectsCacheKey(nextKind, parent, kinds)
	namespace := s.namespaceForRef(parent)

	if s.store != nil {
		var cached []source.Object
		if hit, stale := s.store.Get(scope, cacheKey, &cached); hit {
			for i := range cached {
				if !s.hasChildren(cached[i].Kind) {
					s.rememberValidatedObject(namespace, cached[i].Name)
				}
			}
			if stale {
				parentCopy := cloneObjectRefPtr(parent)
				kindsCopy := slices.Clone(kinds)
				s.store.TriggerRefresh(scope, cacheKey, func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if fresh, err := s.fetchObjectsFromPlugin(bgCtx, nextKind, parentCopy, kindsCopy); err == nil {
						s.store.Set(scope, cacheKey, fresh, metadataSoftTTL, metadataHardTTL)
					}
				})
			}
			return cached, nil
		}
	}

	objects, err := s.fetchObjectsFromPlugin(ctx, nextKind, parent, kinds)
	if err != nil {
		return nil, err
	}
	for i := range objects {
		if !s.hasChildren(objects[i].Kind) {
			s.rememberValidatedObject(namespace, objects[i].Name)
		}
	}
	if s.store != nil {
		s.store.Set(scope, cacheKey, objects, metadataSoftTTL, metadataHardTTL)
	}
	return objects, nil
}

func (s *DatabaseSession) fetchObjectsFromPlugin(ctx context.Context, nextKind source.ObjectKind, parent *source.ObjectRef, kinds []source.ObjectKind) ([]source.Object, error) {
	config := s.pluginConfig(ctx, parent)

	switch nextKind {
	case source.ObjectKindDatabase:
		names, err := s.plugin.GetDatabases(config)
		if err != nil {
			return nil, err
		}
		objects := make([]source.Object, 0, len(names))
		for _, name := range names {
			objects = append(objects, s.makeContainerObject(parent, nextKind, name, nil))
		}
		return source.FilterInternalObjects(s.spec, objects), nil
	case source.ObjectKindSchema:
		names, err := s.plugin.GetAllSchemas(config)
		if err != nil {
			return nil, err
		}
		objects := make([]source.Object, 0, len(names))
		for _, name := range names {
			objects = append(objects, s.makeContainerObject(parent, nextKind, name, nil))
		}
		return source.FilterInternalObjects(s.spec, objects), nil
	default:
		namespace := s.namespaceForRef(parent)
		units, err := s.plugin.GetStorageUnits(config, namespace)
		if err != nil {
			return nil, err
		}
		objects := make([]source.Object, 0, len(units))
		for _, unit := range units {
			kind := s.kindForUnit(nextKind, unit)
			if len(kinds) > 0 && !slices.Contains(kinds, kind) {
				continue
			}
			objectType, _ := s.spec.Contract.ObjectTypeForKind(kind)
			path := appendPath(parent, unit.Name)
			objects = append(objects, source.Object{
				Ref:         source.NewObjectRef(kind, path),
				Kind:        kind,
				Name:        unit.Name,
				Path:        path,
				HasChildren: s.hasChildren(kind),
				Actions:     slices.Clone(objectType.Actions),
				Metadata:    slices.Clone(unit.Attributes),
			})
		}
		return source.FilterInternalObjects(s.spec, objects), nil
	}
}

// GetObject loads one object by reference.
func (s *DatabaseSession) GetObject(ctx context.Context, ref source.ObjectRef) (*source.Object, error) {
	parent := parentForRef(ref)
	objects, err := s.ListObjects(ctx, parent, []source.ObjectKind{ref.Kind})
	if err != nil {
		return nil, err
	}

	for i := range objects {
		if objects[i].Kind == ref.Kind && slices.Equal(objects[i].Path, ref.Path) {
			objectCopy := objects[i]
			return &objectCopy, nil
		}
	}

	return nil, errors.New("source object not found")
}

// ReadRows returns rows for a tabular source object.
func (s *DatabaseSession) ReadRows(ctx context.Context, ref source.ObjectRef, where *query.WhereCondition, sort []*query.SortCondition, pageSize int, pageOffset int) (*source.RowsResult, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionViewRows); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	log.WithFields(map[string]any{
		"source_type": s.spec.ID,
		"connector":   s.spec.Connector,
		"kind":        ref.Kind,
		"path":        slices.Clone(ref.Path),
		"namespace":   namespace,
		"objectName":  name,
		"pageSize":    pageSize,
		"pageOffset":  pageOffset,
		"hasWhere":    where != nil,
		"sortCount":   len(sort),
	}).Debug("Source row read requested")
	if err := s.validateObject(config, namespace, name); err != nil {
		log.WithError(err).WithFields(map[string]any{
			"source_type": s.spec.ID,
			"connector":   s.spec.Connector,
			"kind":        ref.Kind,
			"path":        slices.Clone(ref.Path),
			"namespace":   namespace,
			"objectName":  name,
		}).Debug("Source row read failed validation")
		return nil, err
	}

	rows, err := s.plugin.GetRows(config, &engine.GetRowsRequest{
		Schema:      namespace,
		StorageUnit: name,
		Where:       where,
		Sort:        sort,
		PageSize:    pageSize,
		PageOffset:  pageOffset,
	})
	if err != nil {
		log.WithError(err).WithFields(map[string]any{
			"source_type": s.spec.ID,
			"connector":   s.spec.Connector,
			"kind":        ref.Kind,
			"path":        slices.Clone(ref.Path),
			"namespace":   namespace,
			"objectName":  name,
		}).Debug("Source row read failed in plugin")
		return nil, err
	}

	log.WithFields(map[string]any{
		"source_type": s.spec.ID,
		"connector":   s.spec.Connector,
		"kind":        ref.Kind,
		"path":        slices.Clone(ref.Path),
		"namespace":   namespace,
		"objectName":  name,
		"rowCount":    len(rows.Rows),
		"columnCount": len(rows.Columns),
		"totalCount":  rows.TotalCount,
	}).Debug("Source row read completed")
	return rows, nil
}

// Columns returns columns for one source object.
func (s *DatabaseSession) Columns(ctx context.Context, ref source.ObjectRef) ([]source.Column, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionInspect); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	if cachedColumns, ok := s.cachedColumns(&ref, namespace, name); ok {
		return cachedColumns, nil
	}
	if err := s.validateObject(config, namespace, name); err != nil {
		return nil, err
	}

	columns, err := s.fetchColumnsFromPlugin(config, namespace, name)
	if err != nil {
		return nil, err
	}
	s.cacheColumns(namespace, name, columns)
	return cloneSourceColumns(columns), nil
}

func (s *DatabaseSession) fetchColumnsFromPlugin(config *engine.PluginConfig, namespace string, name string) ([]source.Column, error) {
	columns, err := s.plugin.GetColumnsForTable(config, namespace, name)
	if err != nil {
		return nil, err
	}
	if err := s.plugin.MarkGeneratedColumns(config, namespace, name, columns); err != nil {
		log.WithError(err).Warn("Failed to mark generated columns for source object")
	}
	columns = source.ApplyColumnMetadataFidelity(columns, s.spec.Traits.Metadata.Columns)
	if err := source.ValidateColumns(columns); err != nil {
		return nil, err
	}
	return columns, nil
}

// ColumnsBatch returns columns for multiple source objects.
func (s *DatabaseSession) ColumnsBatch(ctx context.Context, refs []source.ObjectRef) ([]source.ObjectColumns, error) {
	results := make([]source.ObjectColumns, 0, len(refs))
	for _, ref := range refs {
		columns, err := s.Columns(ctx, ref)
		if err != nil {
			continue
		}
		results = append(results, source.ObjectColumns{
			Ref:     ref,
			Columns: columns,
		})
	}
	return results, nil
}

// ColumnConstraints returns per-column constraint metadata for one source
// object.
func (s *DatabaseSession) ColumnConstraints(ctx context.Context, ref source.ObjectRef) (map[string]map[string]any, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionInspect); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	scope := s.baseCacheScope()
	cacheKey := "col_constraints:" + namespace + ":" + name
	if s.store != nil {
		var cached map[string]map[string]any
		if hit, stale := s.store.Get(scope, cacheKey, &cached); hit {
			s.rememberValidatedObject(namespace, name)
			if stale {
				refCopy := source.NewObjectRef(ref.Kind, ref.Path)
				s.store.TriggerRefresh(scope, cacheKey, func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cfg := s.pluginConfig(bgCtx, &refCopy)
					if fresh, err := s.plugin.GetColumnConstraints(cfg, namespace, name); err == nil {
						s.store.Set(scope, cacheKey, fresh, metadataSoftTTL, metadataHardTTL)
					}
				})
			}
			return cached, nil
		}
	}

	if err := s.validateObject(config, namespace, name); err != nil {
		return nil, err
	}

	constraints, err := s.plugin.GetColumnConstraints(config, namespace, name)
	if err != nil {
		return nil, err
	}
	if s.store != nil {
		s.store.Set(scope, cacheKey, constraints, metadataSoftTTL, metadataHardTTL)
	}
	return constraints, nil
}

// FieldConstraints returns normalized source-level field constraints for one
// source object.
func (s *DatabaseSession) FieldConstraints(ctx context.Context, ref source.ObjectRef) ([]source.FieldConstraints, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionInspect); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	scope := s.baseCacheScope()
	cacheKey := "field_constraints:" + namespace + ":" + name
	if s.store != nil {
		var cached []source.FieldConstraints
		if hit, stale := s.store.Get(scope, cacheKey, &cached); hit {
			s.rememberValidatedObject(namespace, name)
			if stale {
				refCopy := source.NewObjectRef(ref.Kind, ref.Path)
				s.store.TriggerRefresh(scope, cacheKey, func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if fresh, err := s.computeFieldConstraints(bgCtx, refCopy, s.pluginConfig(bgCtx, &refCopy), namespace, name); err == nil {
						s.store.Set(scope, cacheKey, fresh, metadataSoftTTL, metadataHardTTL)
					}
				})
			}
			return cached, nil
		}
	}

	if err := s.validateObject(config, namespace, name); err != nil {
		return nil, err
	}

	fields, err := s.computeFieldConstraints(ctx, ref, config, namespace, name)
	if err != nil {
		return nil, err
	}
	if s.store != nil {
		s.store.Set(scope, cacheKey, fields, metadataSoftTTL, metadataHardTTL)
	}
	return fields, nil
}

func (s *DatabaseSession) computeFieldConstraints(ctx context.Context, ref source.ObjectRef, config *engine.PluginConfig, namespace string, name string) ([]source.FieldConstraints, error) {
	columns, err := s.Columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	constraints, err := s.plugin.GetColumnConstraints(config, namespace, name)
	if err != nil {
		return nil, err
	}
	fields := source.MergeFieldConstraintsWithColumns(source.NormalizeFieldConstraints(constraints), columns)

	relationships, err := s.plugin.GetForeignKeyRelationships(config, namespace, name)
	if err != nil {
		return nil, err
	}
	for _, relationship := range relationships {
		if relationship == nil {
			continue
		}
		matched := false
		for i := range fields {
			if !strings.EqualFold(fields[i].Name, relationship.ColumnName) {
				continue
			}
			fields[i].ForeignKey = &source.ForeignKeyDefinition{
				Table:  relationship.ReferencedTable,
				Column: relationship.ReferencedColumn,
			}
			matched = true
			break
		}
		if !matched {
			fields = append(fields, source.FieldConstraints{
				Name: relationship.ColumnName,
				ForeignKey: &source.ForeignKeyDefinition{
					Table:  relationship.ReferencedTable,
					Column: relationship.ReferencedColumn,
				},
			})
		}
	}

	fields = source.ApplyFieldConstraintMetadataFidelity(fields, s.spec.Traits.Metadata.Constraints)
	if err := source.ValidateFieldConstraints(fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// RunQuery executes a query against the source session.
func (s *DatabaseSession) RunQuery(ctx context.Context, sql string, params ...any) (*source.RowsResult, error) {
	if err := s.ensureSurface(source.SurfaceQuery); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, nil)
	res, err := s.plugin.RawExecute(config, sql, params...)
	if err == nil && isLikelySchemaMutatingQuery(sql) {
		s.invalidatePersistentCache()
	}
	return res, err
}

// RunReadOnlyQuery validates and executes SQL through the source's protected path.
func (s *DatabaseSession) RunReadOnlyQuery(ctx context.Context, sql string, params ...any) (*source.RowsResult, error) {
	if err := s.ensureSurface(source.SurfaceQuery); err != nil {
		return nil, err
	}
	if !s.spec.Traits.Query.SupportsReadOnlyExecution {
		return nil, fmt.Errorf("protected read-only execution is not supported for %s", s.spec.Label)
	}
	config, err := plugins.ReadOnlyConfig(s.pluginConfig(ctx, nil), sql)
	if err != nil {
		return nil, err
	}
	return s.plugin.RawExecute(config, sql, params...)
}

// RunQueryStream executes a query through the active source session's
// streaming path when supported.
func (s *DatabaseSession) RunQueryStream(ctx context.Context, sql string, writer source.QueryStreamWriter, params ...any) error {
	if err := s.ensureSurface(source.SurfaceQuery); err != nil {
		return err
	}
	if !s.spec.Traits.Query.SupportsStreaming {
		return fmt.Errorf("streaming queries are not supported for %s", s.spec.Label)
	}
	if writer == nil {
		return errors.New("stream writer is required")
	}

	streamer, ok := s.plugin.PluginFunctions.(engine.QueryStreamer)
	if !ok {
		return fmt.Errorf("streaming queries are not supported for %s", s.spec.Label)
	}

	config := s.pluginConfig(ctx, nil)
	return streamer.StreamRawExecute(config, sql, &sourceQueryStreamWriterAdapter{writer: writer}, params...)
}

// RunScript executes a source-native script against the session.
func (s *DatabaseSession) RunScript(ctx context.Context, script string, multiStatement bool, params ...any) (*source.RowsResult, error) {
	if err := source.ValidateScriptExecutionSupported(s.spec); err != nil {
		return nil, err
	}
	if multiStatement && !s.spec.Traits.Query.SupportsMultiStatement {
		return nil, engine.ErrMultiStatementUnsupported
	}

	config := s.pluginConfig(ctx, nil)
	config.MultiStatement = multiStatement
	res, err := s.plugin.RawExecute(config, script, params...)
	if err == nil && isLikelySchemaMutatingQuery(script) {
		s.invalidatePersistentCache()
	}
	return res, err
}

// ReadGraph returns graph data for a source scope.
func (s *DatabaseSession) ReadGraph(ctx context.Context, ref *source.ObjectRef) ([]source.GraphUnit, error) {
	if err := s.ensureGraphSupported(ref); err != nil {
		return nil, err
	}

	scope := ""
	if ref != nil {
		scope = s.graphScopeForRef(*ref)
	}
	cacheScope := s.baseCacheScope()
	cacheKey := "graph:" + scope
	if s.store != nil {
		var cached []source.GraphUnit
		if hit, stale := s.store.Get(cacheScope, cacheKey, &cached); hit {
			if stale {
				refCopy := cloneObjectRefPtr(ref)
				s.store.TriggerRefresh(cacheScope, cacheKey, func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if fresh, err := s.fetchGraphFromPlugin(bgCtx, refCopy, scope); err == nil {
						s.store.Set(cacheScope, cacheKey, fresh, metadataSoftTTL, metadataHardTTL)
					}
				})
			}
			return cached, nil
		}
	}

	units, err := s.fetchGraphFromPlugin(ctx, ref, scope)
	if err != nil {
		return nil, err
	}
	if s.store != nil {
		s.store.Set(cacheScope, cacheKey, units, metadataSoftTTL, metadataHardTTL)
	}
	return units, nil
}

func (s *DatabaseSession) fetchGraphFromPlugin(ctx context.Context, ref *source.ObjectRef, scope string) ([]source.GraphUnit, error) {
	config := s.pluginConfig(ctx, ref)
	units, err := s.plugin.GetGraph(config, scope)
	if err != nil {
		return nil, err
	}
	units = source.FilterInternalGraphUnits(s.spec, units, s.spec.Contract.DefaultObjectKind)
	units = source.NormalizeGraphMetadata(units, s.spec.Traits.Metadata.Graph)
	if err := source.ValidateGraphUnits(units); err != nil {
		return nil, err
	}
	return units, nil
}

// Reply runs AI chat against the source session.
func (s *DatabaseSession) Reply(ctx context.Context, ref *source.ObjectRef, previousConversation string, prompt string) ([]*source.ChatMessage, error) {
	if err := s.ensureSurface(source.SurfaceChat); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, ref)
	scope := ""
	if ref != nil {
		scope = s.graphScopeForRef(*ref)
	}
	return s.plugin.Chat(config, scope, previousConversation, prompt)
}

// ReplyWithModel runs AI chat against the source session with an explicit model.
func (s *DatabaseSession) ReplyWithModel(ctx context.Context, ref *source.ObjectRef, previousConversation string, prompt string, model *source.ExternalModel) ([]*source.ChatMessage, error) {
	if err := s.ensureSurface(source.SurfaceChat); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, ref)
	config.ExternalModel = model
	scope := ""
	if ref != nil {
		scope = s.graphScopeForRef(*ref)
	}
	return s.plugin.Chat(config, scope, previousConversation, prompt)
}

// CreateObject creates a new source object.
func (s *DatabaseSession) CreateObject(ctx context.Context, parent *source.ObjectRef, name string, fields []source.Record) (bool, error) {
	if err := s.ensureCreateChildSupported(parent); err != nil {
		return false, err
	}

	config := s.pluginConfig(ctx, parent)
	namespace := s.namespaceForRef(parent)
	ok, err := s.plugin.CreateStorageUnit(config, namespace, source.RecordsToObjectDefinition(name, fields))
	if err == nil && ok {
		s.invalidatePersistentCache()
	}
	return ok, err
}

// UpdateObject updates data within an existing source object.
func (s *DatabaseSession) UpdateObject(ctx context.Context, ref source.ObjectRef, values map[string]string, updatedColumns []string) (bool, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionUpdateData); err != nil {
		return false, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	if err := s.validateObject(config, namespace, name); err != nil {
		return false, err
	}
	return s.plugin.UpdateStorageUnit(config, namespace, name, values, updatedColumns)
}

// AddRow inserts a row into a source object.
func (s *DatabaseSession) AddRow(ctx context.Context, ref source.ObjectRef, values []source.Record) (bool, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionInsertData); err != nil {
		return false, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	if err := s.validateObject(config, namespace, name); err != nil {
		return false, err
	}
	return s.plugin.AddRow(config, namespace, name, values)
}

// DeleteRow deletes row/document data from a source object.
func (s *DatabaseSession) DeleteRow(ctx context.Context, ref source.ObjectRef, values map[string]string) (bool, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionDeleteData); err != nil {
		return false, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	if err := s.validateObject(config, namespace, name); err != nil {
		return false, err
	}
	return s.plugin.DeleteRow(config, namespace, name, values)
}

// ClearObjectData removes all rows/documents from a source object while keeping the object structure.
func (s *DatabaseSession) ClearObjectData(ctx context.Context, ref source.ObjectRef) (bool, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionDeleteData); err != nil {
		return false, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	if err := s.validateObject(config, namespace, name); err != nil {
		return false, err
	}
	ok, err := s.plugin.ClearTableData(config, namespace, name)
	if err == nil && ok {
		s.invalidatePersistentCache()
	}
	return ok, err
}

// DropObject drops/removes a source object from the database.
func (s *DatabaseSession) DropObject(ctx context.Context, ref source.ObjectRef) (bool, error) {
	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	if err := s.validateObject(config, namespace, name); err != nil {
		return false, err
	}
	ok, err := s.plugin.DropStorageUnit(config, namespace, name)
	if err == nil && ok {
		s.invalidatePersistentCache()
	}
	return ok, err
}

// IsAvailable verifies that the underlying database plugin can reach the
// source with the current credentials.
func (s *DatabaseSession) IsAvailable(ctx context.Context) bool {
	config := s.pluginConfig(ctx, nil)
	return s.plugin.IsAvailable(ctx, config)
}

// ExportRows streams tabular rows for one source object.
func (s *DatabaseSession) ExportRows(ctx context.Context, ref source.ObjectRef, writer func([]string) error, selectedRows []map[string]any) error {
	if err := s.ensureObjectAction(ref.Kind, source.ActionViewRows); err != nil {
		return err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	return s.plugin.ExportData(config, namespace, name, writer, selectedRows)
}

// ExportRowsNDJSON streams NDJSON rows for one source object.
func (s *DatabaseSession) ExportRowsNDJSON(ctx context.Context, ref source.ObjectRef, writer func(string) error, selectedRows []map[string]any) error {
	if err := s.ensureObjectAction(ref.Kind, source.ActionViewRows); err != nil {
		return err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)

	exporter, ok := s.plugin.PluginFunctions.(interface {
		ExportDataNDJSON(config *engine.PluginConfig, namespace string, objectName string, writer func(string) error, selectedRows []map[string]any) error
	})
	if !ok {
		return errors.New("NDJSON export is not supported")
	}

	return exporter.ExportDataNDJSON(config, namespace, name, writer, selectedRows)
}

// SSLStatus returns the current SSL/TLS status for the active source session.
func (s *DatabaseSession) SSLStatus(ctx context.Context) (*source.SSLStatus, error) {
	config := s.pluginConfig(ctx, nil)
	return s.plugin.GetSSLStatus(config)
}

// ImportData imports parsed tabular data into one source object.
func (s *DatabaseSession) ImportData(ctx context.Context, ref source.ObjectRef, request source.ImportRequest) (*source.ImportResult, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionImportData); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)
	if err := s.validateObject(config, namespace, name); err != nil {
		return nil, err
	}

	columns, err := s.Columns(ctx, ref)
	if err != nil {
		return nil, err
	}

	result, err := importer.Execute(s.plugin, config, &importer.ExecuteRequest{
		Schema:             namespace,
		StorageUnit:        name,
		Mode:               importer.Mode(request.Mode),
		Parsed:             &importer.ParsedFile{Columns: slices.Clone(request.Parsed.Columns), Rows: cloneImportRows(request.Parsed.Rows), Truncated: request.Parsed.Truncated, Sheet: request.Parsed.Sheet},
		Mapping:            importerColumnMappings(request.Mapping),
		AllowAutoGenerated: request.AllowAutoGenerated,
		BatchSize:          request.BatchSize,
		TargetColumns:      columns,
	})
	if err != nil {
		return nil, err
	}

	return &source.ImportResult{RowsImported: result.RowsImported}, nil
}

// GenerateMockData creates synthetic data for one source object.
func (s *DatabaseSession) GenerateMockData(ctx context.Context, ref source.ObjectRef, rowCount int, fkDensityRatio int, overwriteExisting bool) (*source.MockDataGenerationResult, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionGenerateMockData); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)

	generator := src.NewMockDataGenerator(fkDensityRatio)
	result, err := generator.Generate(s.plugin, config, namespace, name, rowCount, overwriteExisting)
	if err != nil {
		return nil, err
	}

	details := make([]source.MockDataTableDetail, 0, len(result.Details))
	for _, detail := range result.Details {
		details = append(details, source.MockDataTableDetail{
			Table:            detail.Table,
			RowsGenerated:    detail.RowsGenerated,
			UsedExistingData: detail.UsedExistingData,
		})
	}

	return &source.MockDataGenerationResult{
		TotalGenerated: result.TotalGenerated,
		Details:        details,
		Warnings:       slices.Clone(result.Warnings),
	}, nil
}

// AnalyzeMockDataDependencies computes the dependency plan for mock-data
// generation on one source object.
func (s *DatabaseSession) AnalyzeMockDataDependencies(ctx context.Context, ref source.ObjectRef, rowCount int, fkDensityRatio int) (*source.MockDataDependencyAnalysis, error) {
	if err := s.ensureObjectAction(ref.Kind, source.ActionGenerateMockData); err != nil {
		return nil, err
	}

	config := s.pluginConfig(ctx, &ref)
	namespace := s.namespaceForRef(&ref)
	name := objectName(ref)

	generator := src.NewMockDataGenerator(fkDensityRatio)
	analysis, err := generator.AnalyzeDependencies(s.plugin, config, namespace, name, rowCount)
	if err != nil {
		return nil, err
	}

	tables := make([]source.MockDataTableDependency, 0, len(analysis.Tables))
	for _, table := range analysis.Tables {
		tables = append(tables, source.MockDataTableDependency{
			Table:            table.Table,
			DependsOn:        slices.Clone(table.DependsOn),
			RowCount:         table.RowCount,
			IsBlocked:        table.IsBlocked,
			UsesExistingData: table.UsesExistingData,
		})
	}

	return &source.MockDataDependencyAnalysis{
		GenerationOrder: slices.Clone(analysis.GenerationOrder),
		Tables:          tables,
		TotalRows:       analysis.TotalRows,
		Warnings:        slices.Clone(analysis.Warnings),
		Error:           analysis.Error,
	}, nil
}

// QuerySuggestions returns source-scoped suggestions for the query UI.
func (s *DatabaseSession) QuerySuggestions(ctx context.Context, ref *source.ObjectRef) ([]source.QuerySuggestion, error) {
	config := s.pluginConfig(ctx, ref)
	scope := s.namespaceForRef(ref)
	units, err := s.plugin.GetStorageUnits(config, scope)
	if err != nil {
		return nil, err
	}
	suggestions := querysuggestions.FromStorageUnits(units)

	mapped := make([]source.QuerySuggestion, 0, len(suggestions))
	for _, suggestion := range suggestions {
		mapped = append(mapped, source.QuerySuggestion{
			Description: suggestion.Description,
			Category:    suggestion.Category,
		})
	}
	return mapped, nil
}

// ListTargetDatabases returns the available database names for the session's target connection.
func (s *DatabaseSession) ListTargetDatabases(ctx context.Context) ([]string, error) {
	config := s.pluginConfig(ctx, nil)
	databases, err := s.plugin.GetDatabases(config)
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			if config.Credentials != nil && config.Credentials.Database != "" {
				return []string{config.Credentials.Database}, nil
			}
			return []string{}, nil
		}
		return nil, err
	}
	return databases, nil
}

// ListTargetSchemas returns the available schema names for the session's target connection.
func (s *DatabaseSession) ListTargetSchemas(ctx context.Context) ([]string, error) {
	config := s.pluginConfig(ctx, nil)
	schemas, err := s.plugin.GetAllSchemas(config)
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return []string{}, nil
		}
		return nil, err
	}
	return schemas, nil
}

// CopyObjects copies the specified storage units from the current source session into targetSession.
func (s *DatabaseSession) CopyObjects(
	ctx context.Context,
	sourceParent *source.ObjectRef,
	targetSession *DatabaseSession,
	targetNamespace string,
	objectNames []string,
	includeData bool,
	existingStrategy engine.CopyExistingStrategy,
) ([]engine.CopyTableDetail, error) {
	if s == nil || s.plugin == nil {
		return nil, errors.New("source database session is not initialized")
	}
	if targetSession == nil || targetSession.plugin == nil {
		return nil, errors.New("target database session is not initialized")
	}
	if s.plugin.Type != targetSession.plugin.Type {
		return nil, fmt.Errorf("cross-type copy from %s to %s is not supported", s.plugin.Type, targetSession.plugin.Type)
	}

	sourceConfig := s.pluginConfig(ctx, sourceParent)
	sourceNamespace := s.namespaceForRef(sourceParent)
	if sourceNamespace == "" && sourceConfig.Credentials != nil {
		sourceNamespace = sourceConfig.Credentials.Database
	}

	targetConfig := targetSession.pluginConfig(ctx, nil)
	resolvedTargetNamespace := strings.TrimSpace(targetNamespace)
	if resolvedTargetNamespace == "" && targetConfig.Credentials != nil {
		resolvedTargetNamespace = targetConfig.Credentials.Database
	}

	details := make([]engine.CopyTableDetail, 0, len(objectNames))
	for _, rawName := range objectNames {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		detail, err := engine.CopyStorageUnit(
			s.plugin,
			sourceConfig,
			sourceNamespace,
			targetConfig,
			resolvedTargetNamespace,
			name,
			includeData,
			existingStrategy,
		)
		if err != nil {
			details = append(details, engine.CopyTableDetail{
				Name:       name,
				Status:     engine.CopyTableStatusFailed,
				RowsCopied: 0,
				Message:    err.Error(),
			})
			continue
		}
		if detail != nil {
			details = append(details, *detail)
		}
	}

	targetSession.invalidatePersistentCache()
	return details, nil
}

func (s *DatabaseSession) engineCredentials(values map[string]string) *engine.Credentials {
	mergedValues := s.credentials.CloneValues()
	for i := range s.spec.ConnectionFields {
		field := &s.spec.ConnectionFields[i]
		if field.DefaultValue == "" {
			continue
		}
		if _, ok := mergedValues[field.Key]; !ok {
			mergedValues[field.Key] = field.DefaultValue
		}
	}
	maps.Copy(mergedValues, values)

	engineCredentials := &engine.Credentials{
		Id:          s.credentials.ID,
		Type:        s.spec.ID,
		AccessToken: s.credentials.AccessToken,
		IsProfile:   s.credentials.IsProfile,
	}

	knownFields := map[string]bool{}
	for i := range s.spec.ConnectionFields {
		field := &s.spec.ConnectionFields[i]
		value := mergedValues[field.Key]
		if value == "" {
			continue
		}
		knownFields[field.Key] = true

		switch field.CredentialField {
		case source.CredentialFieldHostname:
			engineCredentials.Hostname = value
		case source.CredentialFieldUsername:
			engineCredentials.Username = value
		case source.CredentialFieldPassword:
			engineCredentials.Password = value
		case source.CredentialFieldDatabase:
			engineCredentials.Database = value
		case source.CredentialFieldAdvanced:
			advancedKey := field.AdvancedKey
			if advancedKey == "" {
				advancedKey = field.Key
			}
			engineCredentials.Advanced = append(engineCredentials.Advanced, engine.Record{
				Key:   advancedKey,
				Value: value,
			})
		}
	}

	for key, value := range mergedValues {
		if value == "" || knownFields[key] {
			continue
		}
		engineCredentials.Advanced = append(engineCredentials.Advanced, engine.Record{
			Key:   key,
			Value: value,
		})
	}

	return engineCredentials
}

func (s *DatabaseSession) credentialsForRef(ref *source.ObjectRef) *engine.Credentials {
	engineCredentials := s.engineCredentials(nil)
	if ref == nil {
		return engineCredentials
	}

	if databaseName := s.valueForKind(ref.Path, source.ObjectKindDatabase); databaseName != "" {
		engineCredentials.Database = databaseName
	}

	return engineCredentials
}

func (s *DatabaseSession) pluginConfig(ctx context.Context, ref *source.ObjectRef) *engine.PluginConfig {
	config := engine.NewPluginConfig(s.credentialsForRef(ref))
	config.Context = ctx
	return config
}

func (s *DatabaseSession) nextKind(parent *source.ObjectRef) (source.ObjectKind, bool) {
	depth := 0
	if parent != nil {
		depth = len(parent.Path)
	}
	if depth >= len(s.spec.Contract.BrowsePath) {
		return "", false
	}
	return s.spec.Contract.BrowsePath[depth], true
}

func (s *DatabaseSession) namespaceForRef(ref *source.ObjectRef) string {
	if ref == nil {
		return ""
	}

	defaultIndex := slices.Index(s.spec.Contract.BrowsePath, s.spec.Contract.DefaultObjectKind)
	if defaultIndex <= 0 || defaultIndex-1 >= len(ref.Path) {
		return ""
	}
	return ref.Path[defaultIndex-1]
}

func (s *DatabaseSession) graphScopeForRef(ref source.ObjectRef) string {
	if s.spec.Contract.GraphScopeKind == nil {
		return ""
	}
	return s.valueForKind(ref.Path, *s.spec.Contract.GraphScopeKind)
}

func (s *DatabaseSession) valueForKind(path []string, kind source.ObjectKind) string {
	index := slices.Index(s.spec.Contract.BrowsePath, kind)
	if index < 0 || index >= len(path) {
		return ""
	}
	return path[index]
}

func (s *DatabaseSession) makeContainerObject(parent *source.ObjectRef, kind source.ObjectKind, name string, metadata []engine.Record) source.Object {
	objectType, _ := s.spec.Contract.ObjectTypeForKind(kind)
	path := appendPath(parent, name)
	return source.Object{
		Ref:         source.NewObjectRef(kind, path),
		Kind:        kind,
		Name:        name,
		Path:        path,
		HasChildren: s.hasChildren(kind),
		Actions:     slices.Clone(objectType.Actions),
		Metadata:    slices.Clone(metadata),
	}
}

func (s *DatabaseSession) hasChildren(kind source.ObjectKind) bool {
	index := slices.Index(s.spec.Contract.BrowsePath, kind)
	return index >= 0 && index < len(s.spec.Contract.BrowsePath)-1
}

func (s *DatabaseSession) kindForUnit(defaultKind source.ObjectKind, unit engine.StorageUnit) source.ObjectKind {
	for _, attribute := range unit.Attributes {
		if !strings.EqualFold(attribute.Key, "Type") {
			continue
		}

		switch strings.ToUpper(strings.TrimSpace(attribute.Value)) {
		case "TABLE":
			return source.ObjectKindTable
		case "VIEW":
			return source.ObjectKindView
		case "COLLECTION":
			return source.ObjectKindCollection
		case "INDEX":
			return source.ObjectKindIndex
		case "KEY":
			return source.ObjectKindKey
		case "ITEM":
			return source.ObjectKindItem
		}
	}
	return defaultKind
}

func (s *DatabaseSession) validateObject(config *engine.PluginConfig, namespace string, name string) error {
	if s.isValidatedObject(namespace, name) {
		return nil
	}

	log.WithFields(map[string]any{
		"source_type": s.spec.ID,
		"connector":   s.spec.Connector,
		"namespace":   namespace,
		"objectName":  name,
	}).Debug("Validating source object")
	exists, err := s.plugin.StorageUnitExists(config, namespace, name)
	if err != nil {
		log.WithError(err).WithFields(map[string]any{
			"source_type": s.spec.ID,
			"connector":   s.spec.Connector,
			"namespace":   namespace,
			"objectName":  name,
		}).Debug("Source object validation errored")
		return fmt.Errorf("failed to validate source object: %w", err)
	}
	if !exists {
		log.WithFields(map[string]any{
			"source_type": s.spec.ID,
			"connector":   s.spec.Connector,
			"namespace":   namespace,
			"objectName":  name,
		}).Debug("Source object validation reported missing object")
		return fmt.Errorf("source object %q not found", name)
	}
	log.WithFields(map[string]any{
		"source_type": s.spec.ID,
		"connector":   s.spec.Connector,
		"namespace":   namespace,
		"objectName":  name,
	}).Debug("Source object validation succeeded")
	s.rememberValidatedObject(namespace, name)
	return nil
}

func (s *DatabaseSession) cacheKey(namespace string, name string) databaseObjectCacheKey {
	return databaseObjectCacheKey{namespace: namespace, name: name}
}

func (s *DatabaseSession) isValidatedObject(namespace string, name string) bool {
	s.mu.RLock()
	_, ok := s.validated[s.cacheKey(namespace, name)]
	s.mu.RUnlock()
	if ok {
		return true
	}

	if s.store != nil {
		var valid bool
		if hit, _ := s.store.Get(s.baseCacheScope(), "validated:"+namespace+":"+name, &valid); hit && valid {
			s.mu.Lock()
			if s.validated == nil {
				s.validated = map[databaseObjectCacheKey]struct{}{}
			}
			s.validated[s.cacheKey(namespace, name)] = struct{}{}
			s.mu.Unlock()
			return true
		}
	}
	return false
}

func (s *DatabaseSession) rememberValidatedObject(namespace string, name string) {
	s.mu.Lock()
	if s.validated == nil {
		s.validated = map[databaseObjectCacheKey]struct{}{}
	}
	s.validated[s.cacheKey(namespace, name)] = struct{}{}
	s.mu.Unlock()

	if s.store != nil {
		s.store.Set(s.baseCacheScope(), "validated:"+namespace+":"+name, true, metadataSoftTTL, metadataHardTTL)
	}
}

func (s *DatabaseSession) cachedColumns(ref *source.ObjectRef, namespace string, name string) ([]source.Column, bool) {
	s.mu.RLock()
	columns, ok := s.columns[s.cacheKey(namespace, name)]
	s.mu.RUnlock()
	if ok {
		return cloneSourceColumns(columns), true
	}

	if s.store != nil {
		scope := s.baseCacheScope()
		cacheKey := "columns:" + namespace + ":" + name
		var persisted []source.Column
		if hit, stale := s.store.Get(scope, cacheKey, &persisted); hit {
			s.mu.Lock()
			if s.columns == nil {
				s.columns = map[databaseObjectCacheKey][]source.Column{}
			}
			s.columns[s.cacheKey(namespace, name)] = cloneSourceColumns(persisted)
			if s.validated == nil {
				s.validated = map[databaseObjectCacheKey]struct{}{}
			}
			s.validated[s.cacheKey(namespace, name)] = struct{}{}
			s.mu.Unlock()

			if stale && s.plugin != nil {
				refCopy := cloneObjectRefPtr(ref)
				s.store.TriggerRefresh(scope, cacheKey, func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cfg := s.pluginConfig(bgCtx, refCopy)
					if fresh, err := s.fetchColumnsFromPlugin(cfg, namespace, name); err == nil {
						s.store.Set(scope, cacheKey, fresh, metadataSoftTTL, metadataHardTTL)
					}
				})
			}
			return cloneSourceColumns(persisted), true
		}
	}

	return nil, false
}

func (s *DatabaseSession) cacheColumns(namespace string, name string, columns []source.Column) {
	s.mu.Lock()
	if s.columns == nil {
		s.columns = map[databaseObjectCacheKey][]source.Column{}
	}
	s.columns[s.cacheKey(namespace, name)] = cloneSourceColumns(columns)
	if s.validated == nil {
		s.validated = map[databaseObjectCacheKey]struct{}{}
	}
	s.validated[s.cacheKey(namespace, name)] = struct{}{}
	s.mu.Unlock()

	if s.store != nil {
		scope := s.baseCacheScope()
		s.store.Set(scope, "columns:"+namespace+":"+name, columns, metadataSoftTTL, metadataHardTTL)
		s.store.Set(scope, "validated:"+namespace+":"+name, true, metadataSoftTTL, metadataHardTTL)
	}
}

func (s *DatabaseSession) baseCacheScope() string {
	if s.credentials == nil {
		return s.spec.ID + "|" + s.spec.Connector
	}
	creds := s.engineCredentials(nil)
	adv := slices.Clone(creds.Advanced)
	slices.SortFunc(adv, func(a, b engine.Record) int {
		if c := strings.Compare(a.Key, b.Key); c != 0 {
			return c
		}
		return strings.Compare(a.Value, b.Value)
	})
	var b strings.Builder
	b.WriteString(s.spec.ID)
	b.WriteByte('|')
	b.WriteString(s.spec.Connector)
	b.WriteByte('|')
	b.WriteString(creds.Hostname)
	b.WriteByte('|')
	b.WriteString(creds.Username)
	b.WriteByte('|')
	b.WriteString(creds.Database)
	for _, item := range adv {
		if strings.EqualFold(item.Key, "Password") || strings.EqualFold(item.Key, "AccessToken") {
			continue
		}
		b.WriteByte('|')
		b.WriteString(item.Key)
		b.WriteByte('=')
		b.WriteString(item.Value)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return s.spec.ID + ":" + hex.EncodeToString(sum[:12])
}

func (s *DatabaseSession) listObjectsCacheKey(nextKind source.ObjectKind, parent *source.ObjectRef, kinds []source.ObjectKind) string {
	var b strings.Builder
	b.WriteString("list_objects:")
	b.WriteString(string(nextKind))
	b.WriteByte(':')
	if parent != nil {
		b.WriteString(string(parent.Kind))
		b.WriteByte('/')
		b.WriteString(strings.Join(parent.Path, "/"))
	}
	b.WriteByte(':')
	for i, k := range kinds {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(string(k))
	}
	return b.String()
}

func (s *DatabaseSession) invalidatePersistentCache() {
	s.mu.Lock()
	clear(s.validated)
	clear(s.columns)
	s.mu.Unlock()

	if s.store != nil {
		s.store.InvalidateScope(s.baseCacheScope())
	}
}

func cloneObjectRefPtr(ref *source.ObjectRef) *source.ObjectRef {
	if ref == nil {
		return nil
	}
	cloned := source.NewObjectRef(ref.Kind, ref.Path)
	return &cloned
}

func isLikelySchemaMutatingQuery(rawSQL string) bool {
	upper := strings.ToUpper(strings.TrimSpace(rawSQL))
	for _, kw := range []string{"CREATE ", "ALTER ", "DROP ", "TRUNCATE ", "RENAME "} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

func (s *DatabaseSession) ensureSurface(surface source.Surface) error {
	return source.ValidateSurfaceSupported(s.spec, surface)
}

func (s *DatabaseSession) ensureObjectAction(kind source.ObjectKind, action source.Action) error {
	return source.ValidateObjectActionSupported(s.spec, kind, action)
}

func (s *DatabaseSession) ensureCreateChildSupported(parent *source.ObjectRef) error {
	return source.ValidateCreateChildSupported(s.spec, parent)
}

func (s *DatabaseSession) ensureBrowseSupported(parent *source.ObjectRef) error {
	return source.ValidateBrowseSupported(s.spec, parent)
}

func (s *DatabaseSession) ensureGraphSupported(ref *source.ObjectRef) error {
	return source.ValidateGraphSupported(s.spec, ref)
}

func queryLanguagesForSpec(spec source.TypeSpec) []string {
	if !spec.Contract.SupportsSurface(source.SurfaceQuery) {
		return []string{}
	}
	switch engine.DatabaseType(spec.Connector) {
	case engine.DatabaseType_MongoDB:
		return []string{"mongodb"}
	case engine.DatabaseType_Redis:
		return []string{"redis"}
	default:
		return []string{"sql"}
	}
}

func cloneAliasMap(aliasMap map[string]string) map[string]string {
	if len(aliasMap) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(aliasMap))
	maps.Copy(cloned, aliasMap)
	return cloned
}

func appendPath(parent *source.ObjectRef, name string) []string {
	if parent == nil {
		return []string{name}
	}
	path := slices.Clone(parent.Path)
	path = append(path, name)
	return path
}

func parentForRef(ref source.ObjectRef) *source.ObjectRef {
	if len(ref.Path) == 0 {
		return nil
	}

	if len(ref.Path) == 1 {
		return nil
	}

	parent := source.NewObjectRef(ref.Kind, ref.Path[:len(ref.Path)-1])
	return &parent
}

func objectName(ref source.ObjectRef) string {
	if len(ref.Path) == 0 {
		return ""
	}
	return ref.Path[len(ref.Path)-1]
}

func cloneImportRows(rows [][]string) [][]string {
	cloned := make([][]string, 0, len(rows))
	for _, row := range rows {
		cloned = append(cloned, slices.Clone(row))
	}
	return cloned
}

func cloneSourceColumns(columns []source.Column) []source.Column {
	if columns == nil {
		return nil
	}

	cloned := make([]source.Column, 0, len(columns))
	for _, column := range columns {
		cloned = append(cloned, source.Column{
			Type:             column.Type,
			Name:             column.Name,
			Comment:          cloneStringPointer(column.Comment),
			MetadataFidelity: column.MetadataFidelity,
			IsNullable:       column.IsNullable,
			IsPrimary:        column.IsPrimary,
			IsAutoIncrement:  column.IsAutoIncrement,
			IsComputed:       column.IsComputed,
			IsForeignKey:     column.IsForeignKey,
			ReferencedTable:  cloneStringPointer(column.ReferencedTable),
			ReferencedColumn: cloneStringPointer(column.ReferencedColumn),
			Length:           cloneIntPointer(column.Length),
			Precision:        cloneIntPointer(column.Precision),
			Scale:            cloneIntPointer(column.Scale),
		})
	}

	return cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

func importerColumnMappings(mappings []source.ImportColumnMapping) []importer.ColumnMapping {
	if len(mappings) == 0 {
		return nil
	}

	converted := make([]importer.ColumnMapping, 0, len(mappings))
	for _, mapping := range mappings {
		converted = append(converted, importer.ColumnMapping{
			SourceColumn: mapping.SourceColumn,
			TargetColumn: mapping.TargetColumn,
			Skip:         mapping.Skip,
		})
	}
	return converted
}
