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

// Package tdengine implements the WhoDB database plugin for TDengine over its REST SQL API.
package tdengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/clidey/whodb/core/src/common"
	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/query"
	sourcecatalogspecs "github.com/clidey/whodb/core/src/sourcecatalog/specs"
)

var supportedOperators = sourcecatalogspecs.TDengineSupportedOperators

// TDenginePlugin implements engine.PluginFunctions for TDengine time-series databases.
type TDenginePlugin struct {
	engine.BasePlugin
}

// IsAvailable checks whether the TDengine server is reachable and accepts credentials.
func (p *TDenginePlugin) IsAvailable(ctx context.Context, config *engine.PluginConfig) bool {
	client, err := newTDengineClient(config)
	if err != nil {
		return false
	}
	_, err = client.query(ctx, "", "SHOW DATABASES")
	return err == nil
}

// GetDatabases returns all databases available on the TDengine instance.
func (p *TDenginePlugin) GetDatabases(config *engine.PluginConfig) ([]string, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return nil, err
	}
	resp, err := client.query(config.OperationContext(), "", "SHOW DATABASES")
	if err != nil {
		return nil, err
	}

	databases := make([]string, 0, len(resp.Data))
	for _, row := range resp.Data {
		if len(row) == 0 {
			continue
		}
		if name := formatCell(row[0]); name != "" {
			databases = append(databases, name)
		}
	}
	return databases, nil
}

// GetAllSchemas is unsupported in TDengine because TDengine uses databases directly.
func (p *TDenginePlugin) GetAllSchemas(_ *engine.PluginConfig) ([]string, error) {
	return nil, errors.ErrUnsupported
}

type tdengineFieldMeta struct {
	Name      string
	Type      string
	Length    *int
	IsPrimary bool
	IsTag     bool
}

// GetStorageUnits lists supertables (STABLE) and tables in the requested TDengine database.
func (p *TDenginePlugin) GetStorageUnits(config *engine.PluginConfig, schema string) ([]engine.StorageUnit, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return nil, err
	}
	dbName := resolveDatabaseName(schema, config)
	if dbName == "" {
		return []engine.StorageUnit{}, nil
	}

	ctx := config.OperationContext()
	seen := make(map[string]bool)
	units := make([]engine.StorageUnit, 0)

	stablesResp, err := client.query(ctx, dbName, fmt.Sprintf("SHOW %s.STABLES", escapeIdent(dbName)))
	if err != nil {
		stablesResp, err = client.query(ctx, dbName, "SHOW STABLES")
	}
	if err == nil && stablesResp != nil {
		for _, row := range stablesResp.Data {
			if len(row) == 0 {
				continue
			}
			name := formatCell(row[0])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			attrs := []engine.Record{
				{Key: "Type", Value: "TABLE"},
				{Key: "Kind", Value: "SUPER TABLE"},
			}
			if len(row) >= 5 {
				if childCount := formatCell(row[4]); childCount != "" {
					attrs = append(attrs, engine.Record{Key: "Child Tables", Value: childCount})
				}
			}
			units = append(units, engine.StorageUnit{
				Name:       name,
				Attributes: attrs,
			})
		}
	}

	tablesResp, err := client.query(ctx, dbName, fmt.Sprintf("SHOW %s.TABLES", escapeIdent(dbName)))
	if err != nil {
		tablesResp, err = client.query(ctx, dbName, "SHOW TABLES")
	}
	if err != nil {
		if len(units) > 0 || isDatabaseMissingError(err) {
			return units, nil
		}
		return nil, err
	}
	for _, row := range tablesResp.Data {
		if len(row) == 0 {
			continue
		}
		name := formatCell(row[0])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		attrs := []engine.Record{
			{Key: "Type", Value: "TABLE"},
		}
		if len(row) >= 5 {
			if stableName := strings.TrimSpace(formatCell(row[4])); stableName != "" {
				attrs = append(attrs,
					engine.Record{Key: "Kind", Value: "CHILD TABLE"},
					engine.Record{Key: "Super Table", Value: stableName},
				)
			} else {
				attrs = append(attrs, engine.Record{Key: "Kind", Value: "NORMAL TABLE"})
			}
		}
		units = append(units, engine.StorageUnit{
			Name:       name,
			Attributes: attrs,
		})
	}

	return units, nil
}

// StorageUnitExists verifies whether a table or supertable exists in the TDengine database.
func (p *TDenginePlugin) StorageUnitExists(config *engine.PluginConfig, schema string, storageUnit string) (bool, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return false, err
	}
	dbName := resolveDatabaseName(schema, config)
	if strings.TrimSpace(storageUnit) == "" {
		return false, nil
	}

	fields, err := describeTDengineTable(config.OperationContext(), client, dbName, storageUnit)
	if err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "not exist") || strings.Contains(lower, "does not exist") || strings.Contains(lower, "invalid table") {
			return false, nil
		}
		return false, err
	}
	return len(fields) > 0, nil
}

func describeTDengineTable(ctx context.Context, client *tdengineClient, dbName string, storageUnit string) ([]tdengineFieldMeta, error) {
	resp, err := client.query(ctx, dbName, fmt.Sprintf("DESCRIBE %s", qualifyTable(dbName, storageUnit)))
	if err != nil {
		return nil, err
	}

	fields := make([]tdengineFieldMeta, 0, len(resp.Data))
	for idx, row := range resp.Data {
		if len(row) < 2 {
			continue
		}
		colName := formatCell(row[0])
		colType := strings.ToUpper(strings.TrimSpace(formatCell(row[1])))
		var colLen *int
		if len(row) >= 3 {
			if n, err := strconv.Atoi(formatCell(row[2])); err == nil && n > 0 && isLengthType(colType) {
				colLen = &n
			}
		}
		note := ""
		if len(row) >= 4 {
			note = strings.ToUpper(strings.TrimSpace(formatCell(row[3])))
		}
		isPrimary := idx == 0 && strings.Contains(colType, "TIMESTAMP")
		fields = append(fields, tdengineFieldMeta{
			Name:      colName,
			Type:      colType,
			Length:    colLen,
			IsPrimary: isPrimary,
			IsTag:     note == "TAG",
		})
	}
	return fields, nil
}

// GetColumnsForTable returns the column and tag definitions for a TDengine table or supertable.
func (p *TDenginePlugin) GetColumnsForTable(config *engine.PluginConfig, schema string, storageUnit string) ([]engine.Column, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return nil, err
	}
	dbName := resolveDatabaseName(schema, config)
	fields, err := describeTDengineTable(config.OperationContext(), client, dbName, storageUnit)
	if err != nil {
		return nil, err
	}

	columns := make([]engine.Column, 0, len(fields))
	for _, f := range fields {
		columns = append(columns, engine.Column{
			Name:       f.Name,
			Type:       f.Type,
			IsPrimary:  f.IsPrimary,
			IsNullable: !f.IsPrimary,
			Length:     f.Length,
		})
	}
	return columns, nil
}

// CreateStorageUnit creates a new TDengine table in the target database.
func (p *TDenginePlugin) CreateStorageUnit(config *engine.PluginConfig, schema string, definition engine.ObjectDefinition) (bool, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return false, err
	}
	dbName := resolveDatabaseName(schema, config)
	tableName := strings.TrimSpace(definition.Name)
	if tableName == "" {
		return false, errors.New("table name is required")
	}

	cols := make([]engine.ColumnDefinition, 0, len(definition.Columns)+1)
	tsIdx := -1
	for i, c := range definition.Columns {
		if strings.Contains(strings.ToUpper(strings.TrimSpace(c.Type)), "TIMESTAMP") {
			tsIdx = i
			break
		}
	}
	if tsIdx > 0 {
		cols = append(cols, definition.Columns[tsIdx])
		for i, c := range definition.Columns {
			if i != tsIdx {
				cols = append(cols, c)
			}
		}
	} else if tsIdx == 0 {
		cols = append(cols, definition.Columns...)
	} else {
		cols = append(cols, engine.ColumnDefinition{Name: "ts", Type: "TIMESTAMP"})
		cols = append(cols, definition.Columns...)
	}

	colDefs := make([]string, 0, len(cols))
	for _, col := range cols {
		colName := strings.TrimSpace(col.Name)
		if colName == "" {
			continue
		}
		colType := formatTDengineColumnType(col.Type)
		colDefs = append(colDefs, fmt.Sprintf("%s %s", escapeIdent(colName), colType))
	}
	if len(colDefs) == 0 {
		colDefs = append(colDefs, "`ts` TIMESTAMP")
	}

	stmt := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", qualifyTable(dbName, tableName), strings.Join(colDefs, ", "))
	_, err = client.query(config.OperationContext(), dbName, stmt)
	if err != nil {
		return false, err
	}
	return true, nil
}

// AddStorageUnit creates a new TDengine table from legacy field records.
func (p *TDenginePlugin) AddStorageUnit(config *engine.PluginConfig, schema string, storageUnit string, fields []engine.Record) (bool, error) {
	return p.CreateStorageUnit(config, schema, engine.RecordsToObjectDefinition(storageUnit, fields))
}

// AddRow inserts a single time-series row into a TDengine table or supertable.
func (p *TDenginePlugin) AddRow(config *engine.PluginConfig, schema string, storageUnit string, values []engine.Record) (bool, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return false, err
	}
	dbName := resolveDatabaseName(schema, config)
	ctx := config.OperationContext()

	fields, err := describeTDengineTable(ctx, client, dbName, storageUnit)
	if err != nil {
		return false, err
	}

	stmt, err := buildTDengineInsertSQL(ctx, client, dbName, storageUnit, fields, values)
	if err != nil {
		return false, err
	}
	_, err = client.query(ctx, dbName, stmt)
	if err != nil {
		return false, err
	}
	return true, nil
}

// BulkAddRows inserts multiple time-series rows into a TDengine table or supertable.
func (p *TDenginePlugin) BulkAddRows(config *engine.PluginConfig, schema string, storageUnit string, rows [][]engine.Record) (bool, error) {
	for _, row := range rows {
		if _, err := p.AddRow(config, schema, storageUnit, row); err != nil {
			return false, err
		}
	}
	return true, nil
}

// DeleteRow deletes time-series rows matching the timestamp (and tag) predicates from a TDengine table.
func (p *TDenginePlugin) DeleteRow(config *engine.PluginConfig, schema string, storageUnit string, values map[string]string) (bool, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return false, err
	}
	dbName := resolveDatabaseName(schema, config)
	ctx := config.OperationContext()

	fields, err := describeTDengineTable(ctx, client, dbName, storageUnit)
	if err != nil {
		return false, err
	}

	predicates := make([]string, 0, len(fields))
	hasTimestamp := false
	for _, f := range fields {
		if !f.IsPrimary && !f.IsTag {
			continue
		}
		rawVal, ok := values[f.Name]
		if !ok || strings.TrimSpace(rawVal) == "" {
			continue
		}
		if f.IsPrimary {
			hasTimestamp = true
		}
		predicates = append(predicates, fmt.Sprintf("%s = %s", escapeIdent(f.Name), formatSQLValue(strings.TrimSpace(rawVal), f.Type)))
	}
	if !hasTimestamp {
		return false, errors.New("TDengine DELETE requires the primary TIMESTAMP column value")
	}

	stmt := fmt.Sprintf("DELETE FROM %s WHERE %s", qualifyTable(dbName, storageUnit), strings.Join(predicates, " AND "))
	_, err = client.query(ctx, dbName, stmt)
	if err != nil {
		return false, err
	}
	return true, nil
}

// ClearTableData removes all time-series data from a TDengine table or supertable.
func (p *TDenginePlugin) ClearTableData(config *engine.PluginConfig, schema string, storageUnit string) (bool, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return false, err
	}
	dbName := resolveDatabaseName(schema, config)
	ctx := config.OperationContext()

	tsCol := "ts"
	if fields, err := describeTDengineTable(ctx, client, dbName, storageUnit); err == nil && len(fields) > 0 {
		tsCol = fields[0].Name
	}

	stmt := fmt.Sprintf("DELETE FROM %s WHERE %s >= 0", qualifyTable(dbName, storageUnit), escapeIdent(tsCol))
	_, err = client.query(ctx, dbName, stmt)
	if err != nil {
		return false, err
	}
	return true, nil
}

// DropStorageUnit drops a table or supertable from TDengine.
func (p *TDenginePlugin) DropStorageUnit(config *engine.PluginConfig, schema string, storageUnit string) (bool, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return false, err
	}
	dbName := resolveDatabaseName(schema, config)
	ctx := config.OperationContext()

	stmt := fmt.Sprintf("DROP TABLE IF EXISTS %s", qualifyTable(dbName, storageUnit))
	if _, err := client.query(ctx, dbName, stmt); err != nil {
		stmt = fmt.Sprintf("DROP STABLE IF EXISTS %s", qualifyTable(dbName, storageUnit))
		if _, err2 := client.query(ctx, dbName, stmt); err2 != nil {
			return false, err
		}
	}
	return true, nil
}

// GetGraph returns supertable-to-child-table relationships for the TDengine database.
func (p *TDenginePlugin) GetGraph(config *engine.PluginConfig, schema string) ([]engine.GraphUnit, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return nil, err
	}
	dbName := resolveDatabaseName(schema, config)
	if dbName == "" {
		return []engine.GraphUnit{}, nil
	}
	ctx := config.OperationContext()

	stables := make([]string, 0)
	stableSet := make(map[string]bool)
	if stablesResp, err := client.query(ctx, dbName, "SHOW STABLES"); err == nil {
		for _, row := range stablesResp.Data {
			if len(row) == 0 {
				continue
			}
			if name := formatCell(row[0]); name != "" && !stableSet[name] {
				stableSet[name] = true
				stables = append(stables, name)
			}
		}
	}

	childrenByStable := make(map[string][]string)
	standaloneTables := make([]string, 0)
	if tablesResp, err := client.query(ctx, dbName, "SHOW TABLES"); err == nil {
		for _, row := range tablesResp.Data {
			if len(row) == 0 {
				continue
			}
			tableName := formatCell(row[0])
			if tableName == "" {
				continue
			}
			stableName := ""
			if len(row) >= 5 {
				stableName = strings.TrimSpace(formatCell(row[4]))
			}
			if stableName != "" && stableSet[stableName] {
				if len(childrenByStable[stableName]) < 40 {
					childrenByStable[stableName] = append(childrenByStable[stableName], tableName)
				}
			} else if len(standaloneTables) < 50 {
				standaloneTables = append(standaloneTables, tableName)
			}
		}
	}

	graphUnits := make([]engine.GraphUnit, 0, len(stables)+len(standaloneTables))
	for _, stableName := range stables {
		children := childrenByStable[stableName]
		relations := make([]engine.GraphUnitRelationship, 0, len(children))
		for _, childName := range children {
			relations = append(relations, engine.GraphUnitRelationship{
				Name:             childName,
				RelationshipType: engine.GraphUnitRelationshipTypeOneToMany,
			})
		}
		graphUnits = append(graphUnits, engine.GraphUnit{
			Unit: engine.StorageUnit{
				Name: stableName,
				Attributes: []engine.Record{
					{Key: "Type", Value: "TABLE"},
					{Key: "Kind", Value: "SUPER TABLE"},
				},
			},
			Relations: relations,
		})
		for _, childName := range children {
			graphUnits = append(graphUnits, engine.GraphUnit{
				Unit: engine.StorageUnit{
					Name: childName,
					Attributes: []engine.Record{
						{Key: "Type", Value: "TABLE"},
						{Key: "Kind", Value: "CHILD TABLE"},
						{Key: "Super Table", Value: stableName},
					},
				},
			})
		}
	}
	for _, tableName := range standaloneTables {
		graphUnits = append(graphUnits, engine.GraphUnit{
			Unit: engine.StorageUnit{
				Name: tableName,
				Attributes: []engine.Record{
					{Key: "Type", Value: "TABLE"},
					{Key: "Kind", Value: "NORMAL TABLE"},
				},
			},
		})
	}
	return graphUnits, nil
}

// GetTableSchema returns the CREATE statement for a TDengine supertable or table.
func (p *TDenginePlugin) GetTableSchema(config *engine.PluginConfig, schema string, storageUnit string) (string, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return "", err
	}
	dbName := resolveDatabaseName(schema, config)
	target := qualifyTable(dbName, storageUnit)
	ctx := config.OperationContext()

	for _, stmt := range []string{
		fmt.Sprintf("SHOW CREATE STABLE %s", target),
		fmt.Sprintf("SHOW CREATE TABLE %s", target),
	} {
		resp, err := client.query(ctx, dbName, stmt)
		if err == nil && len(resp.Data) > 0 {
			row := resp.Data[0]
			if len(row) >= 2 {
				return formatCell(row[1]), nil
			}
			if len(row) == 1 {
				return formatCell(row[0]), nil
			}
		}
	}
	return "", fmt.Errorf("unable to inspect create statement for %s", storageUnit)
}

// GetRows queries paginated rows from a TDengine table or supertable.
func (p *TDenginePlugin) GetRows(config *engine.PluginConfig, req *engine.GetRowsRequest) (*engine.GetRowsResult, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return nil, err
	}
	dbName := resolveDatabaseName(req.Schema, config)
	target := qualifyTable(dbName, req.StorageUnit)
	ctx := config.OperationContext()

	whereClause, err := buildTDengineWhere(req.Where)
	if err != nil {
		return nil, err
	}
	orderClause := buildTDengineOrderBy(req.Sort)

	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	pageOffset := max(req.PageOffset, 0)

	sqlQuery := fmt.Sprintf("SELECT * FROM %s%s%s LIMIT %d OFFSET %d", target, whereClause, orderClause, pageSize, pageOffset)
	resp, err := client.query(ctx, dbName, sqlQuery)
	if err != nil {
		return nil, err
	}

	result := responseToRowsResult(resp)
	if len(result.Columns) == 0 {
		if cols, err := p.GetColumnsForTable(config, req.Schema, req.StorageUnit); err == nil {
			result.Columns = cols
		}
	}
	result.DisableUpdate = true

	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM %s%s", target, whereClause)
	if countResp, err := client.query(ctx, dbName, countSQL); err == nil && len(countResp.Data) > 0 && len(countResp.Data[0]) > 0 {
		if total, err := strconv.ParseInt(formatCell(countResp.Data[0][0]), 10, 64); err == nil {
			result.TotalCount = total
		}
	}
	if result.TotalCount == 0 && len(result.Rows) > 0 {
		result.TotalCount = int64(len(result.Rows))
	}

	return result, nil
}

// GetRowCount returns the total row count for a TDengine table or supertable.
func (p *TDenginePlugin) GetRowCount(config *engine.PluginConfig, schema string, storageUnit string, where *query.WhereCondition) (int64, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return 0, err
	}
	dbName := resolveDatabaseName(schema, config)
	target := qualifyTable(dbName, storageUnit)
	whereClause, err := buildTDengineWhere(where)
	if err != nil {
		return 0, err
	}

	resp, err := client.query(config.OperationContext(), dbName, fmt.Sprintf("SELECT COUNT(*) FROM %s%s", target, whereClause))
	if err != nil {
		return 0, err
	}
	if len(resp.Data) == 0 || len(resp.Data[0]) == 0 {
		return 0, nil
	}
	return strconv.ParseInt(formatCell(resp.Data[0][0]), 10, 64)
}

// RawExecute executes a raw SQL query against TDengine via its REST SQL API.
func (p *TDenginePlugin) RawExecute(config *engine.PluginConfig, query string, _ ...any) (*engine.GetRowsResult, error) {
	client, err := newTDengineClient(config)
	if err != nil {
		return nil, err
	}
	resp, err := client.query(config.OperationContext(), config.Credentials.Database, query)
	if err != nil {
		return nil, err
	}
	result := responseToRowsResult(resp)
	result.DisableUpdate = true
	return result, nil
}

// ExportData writes tabular data from a TDengine table to the provided CSV/row writer.
func (p *TDenginePlugin) ExportData(config *engine.PluginConfig, schema string, storageUnit string, writer func([]string) error, selectedRows []map[string]any) error {
	if len(selectedRows) > 0 {
		columns, err := p.GetColumnsForTable(config, schema, storageUnit)
		if err != nil {
			return err
		}
		headers := make([]string, len(columns))
		for i, col := range columns {
			headers[i] = col.Name
		}
		if err := writer(headers); err != nil {
			return err
		}
		for _, rowMap := range selectedRows {
			row := make([]string, len(headers))
			for i, colName := range headers {
				row[i] = formatCell(rowMap[colName])
			}
			if err := writer(row); err != nil {
				return err
			}
		}
		return nil
	}

	res, err := p.GetRows(config, &engine.GetRowsRequest{
		Schema:      schema,
		StorageUnit: storageUnit,
		PageSize:    10000,
		PageOffset:  0,
	})
	if err != nil {
		return err
	}
	headers := make([]string, len(res.Columns))
	for i, col := range res.Columns {
		headers[i] = col.Name
	}
	if err := writer(headers); err != nil {
		return err
	}
	for _, row := range res.Rows {
		if err := writer(row); err != nil {
			return err
		}
	}
	return nil
}

// GetSupportedOperators returns the SQL filter operators supported by TDengine.
func (p *TDenginePlugin) GetSupportedOperators() map[string]string {
	return supportedOperators
}

// NormalizeType normalizes a TDengine type alias to its canonical type name.
func (p *TDenginePlugin) NormalizeType(typeName string) string {
	return common.NormalizeTypeWithMap(typeName, sourcecatalogspecs.TDengineAliasMap)
}

func resolveDatabaseName(schema string, config *engine.PluginConfig) string {
	if s := strings.TrimSpace(schema); s != "" {
		return s
	}
	if config != nil && config.Credentials != nil {
		return strings.TrimSpace(config.Credentials.Database)
	}
	return ""
}

func escapeIdent(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func qualifyTable(database string, table string) string {
	if strings.TrimSpace(database) == "" {
		return escapeIdent(table)
	}
	return escapeIdent(database) + "." + escapeIdent(table)
}

func escapeLiteral(val string) string {
	return "'" + strings.ReplaceAll(val, "'", "''") + "'"
}

func buildTDengineWhere(where *query.WhereCondition) (string, error) {
	if where == nil {
		return "", nil
	}
	expr, err := buildTDengineCondition(where)
	if err != nil {
		return "", err
	}
	if expr == "" {
		return "", nil
	}
	return " WHERE " + expr, nil
}

func buildTDengineCondition(cond *query.WhereCondition) (string, error) {
	if cond == nil {
		return "", nil
	}
	switch cond.Type {
	case query.WhereConditionTypeAtomic:
		if cond.Atomic == nil {
			return "", nil
		}
		op, ok := supportedOperators[strings.ToUpper(strings.TrimSpace(cond.Atomic.Operator))]
		if !ok {
			return "", fmt.Errorf("unsupported TDengine operator: %s", cond.Atomic.Operator)
		}
		col := escapeIdent(cond.Atomic.Key)
		val := strings.TrimSpace(cond.Atomic.Value)
		switch op {
		case "IS NULL", "IS NOT NULL":
			return fmt.Sprintf("%s %s", col, op), nil
		case "IN", "NOT IN":
			parts := strings.Split(val, ",")
			quoted := make([]string, 0, len(parts))
			for _, part := range parts {
				quoted = append(quoted, formatSQLValue(strings.TrimSpace(part), cond.Atomic.ColumnType))
			}
			return fmt.Sprintf("%s %s (%s)", col, op, strings.Join(quoted, ", ")), nil
		case "BETWEEN", "NOT BETWEEN":
			parts := strings.SplitN(val, ",", 2)
			if len(parts) != 2 {
				return "", errors.New("BETWEEN operator requires two comma-separated values")
			}
			return fmt.Sprintf(
				"%s %s %s AND %s",
				col,
				op,
				formatSQLValue(strings.TrimSpace(parts[0]), cond.Atomic.ColumnType),
				formatSQLValue(strings.TrimSpace(parts[1]), cond.Atomic.ColumnType),
			), nil
		default:
			return fmt.Sprintf("%s %s %s", col, op, formatSQLValue(val, cond.Atomic.ColumnType)), nil
		}
	case query.WhereConditionTypeAnd:
		if cond.And == nil || len(cond.And.Children) == 0 {
			return "", nil
		}
		parts := make([]string, 0, len(cond.And.Children))
		for _, child := range cond.And.Children {
			sub, err := buildTDengineCondition(child)
			if err != nil {
				return "", err
			}
			if sub != "" {
				parts = append(parts, sub)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	case query.WhereConditionTypeOr:
		if cond.Or == nil || len(cond.Or.Children) == 0 {
			return "", nil
		}
		parts := make([]string, 0, len(cond.Or.Children))
		for _, child := range cond.Or.Children {
			sub, err := buildTDengineCondition(child)
			if err != nil {
				return "", err
			}
			if sub != "" {
				parts = append(parts, sub)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	default:
		return "", nil
	}
}

func formatSQLValue(val string, columnType string) string {
	upper := strings.ToUpper(strings.TrimSpace(columnType))
	if strings.Contains(upper, "INT") || strings.Contains(upper, "FLOAT") || strings.Contains(upper, "DOUBLE") {
		if _, err := strconv.ParseFloat(val, 64); err == nil {
			return val
		}
	}
	if upper == "BOOL" || upper == "BOOLEAN" {
		if strings.EqualFold(val, "true") || val == "1" {
			return "true"
		}
		if strings.EqualFold(val, "false") || val == "0" {
			return "false"
		}
	}
	return escapeLiteral(val)
}

func buildTDengineOrderBy(sorts []*query.SortCondition) string {
	if len(sorts) == 0 {
		return ""
	}
	clauses := make([]string, 0, len(sorts))
	for _, s := range sorts {
		if s == nil || strings.TrimSpace(s.Column) == "" {
			continue
		}
		dir := "ASC"
		if s.Direction == query.SortDirectionDesc {
			dir = "DESC"
		}
		clauses = append(clauses, fmt.Sprintf("%s %s", escapeIdent(s.Column), dir))
	}
	if len(clauses) == 0 {
		return ""
	}
	return " ORDER BY " + strings.Join(clauses, ", ")
}

func responseToRowsResult(resp *tdengineResponse) *engine.GetRowsResult {
	columns := make([]engine.Column, 0, len(resp.ColumnMeta))
	for idx, meta := range resp.ColumnMeta {
		if len(meta) == 0 {
			continue
		}
		colName := formatCell(meta[0])
		colType := "VARCHAR"
		if len(meta) >= 2 {
			if t := strings.ToUpper(strings.TrimSpace(formatCell(meta[1]))); t != "" {
				colType = t
			}
		}
		var colLen *int
		if len(meta) >= 3 {
			if n, err := strconv.Atoi(formatCell(meta[2])); err == nil && n > 0 && isLengthType(colType) {
				colLen = &n
			}
		}
		isPrimary := idx == 0 && strings.Contains(colType, "TIMESTAMP")
		columns = append(columns, engine.Column{
			Name:       colName,
			Type:       colType,
			IsPrimary:  isPrimary,
			IsNullable: !isPrimary,
			Length:     colLen,
		})
	}

	rows := make([][]string, 0, len(resp.Data))
	for _, dataRow := range resp.Data {
		row := make([]string, len(columns))
		for i := range columns {
			if i < len(dataRow) {
				row[i] = formatCell(dataRow[i])
			}
		}
		rows = append(rows, row)
	}

	return &engine.GetRowsResult{
		Columns:    columns,
		Rows:       rows,
		TotalCount: int64(len(rows)),
	}
}

func formatCell(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	case json.Number:
		return val.String()
	default:
		if b, err := json.Marshal(val); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", val)
	}
}

func isLengthType(colType string) bool {
	switch strings.ToUpper(strings.TrimSpace(colType)) {
	case "VARCHAR", "BINARY", "NCHAR", "VARBINARY":
		return true
	default:
		return false
	}
}

func isDatabaseMissingError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "database not specified") ||
		strings.Contains(lower, "database not exist") ||
		strings.Contains(lower, "invalid database")
}

func formatTDengineColumnType(rawType string) string {
	upper := strings.ToUpper(strings.TrimSpace(rawType))
	if upper == "" {
		return "VARCHAR(64)"
	}
	if isLengthType(upper) && !strings.Contains(upper, "(") {
		return upper + "(64)"
	}
	return upper
}

func isTDengineSupertable(ctx context.Context, client *tdengineClient, dbName string, storageUnit string) bool {
	resp, err := client.query(ctx, dbName, fmt.Sprintf("SHOW CREATE STABLE %s", qualifyTable(dbName, storageUnit)))
	return err == nil && len(resp.Data) > 0
}

func buildTDengineInsertSQL(
	ctx context.Context,
	client *tdengineClient,
	dbName string,
	storageUnit string,
	fields []tdengineFieldMeta,
	values []engine.Record,
) (string, error) {
	valMap := make(map[string]string, len(values))
	for _, rec := range values {
		valMap[rec.Key] = rec.Value
	}

	isStable := isTDengineSupertable(ctx, client, dbName, storageUnit)
	dataCols := make([]string, 0, len(fields))
	dataVals := make([]string, 0, len(fields))
	tagCols := make([]string, 0, len(fields))
	tagVals := make([]string, 0, len(fields))
	var tagSlugParts []string

	for _, f := range fields {
		raw, exists := valMap[f.Name]
		trimmed := strings.TrimSpace(raw)
		if f.IsTag {
			if !isStable {
				continue
			}
			tagCols = append(tagCols, escapeIdent(f.Name))
			if !exists || trimmed == "" {
				tagVals = append(tagVals, "NULL")
			} else {
				tagVals = append(tagVals, formatSQLValue(trimmed, f.Type))
				tagSlugParts = append(tagSlugParts, sanitizeTableIdentPart(trimmed))
			}
			continue
		}

		if f.IsPrimary {
			dataCols = append(dataCols, escapeIdent(f.Name))
			if !exists || trimmed == "" || strings.EqualFold(trimmed, "now") || strings.EqualFold(trimmed, "now()") {
				dataVals = append(dataVals, "NOW")
			} else {
				dataVals = append(dataVals, formatSQLValue(trimmed, f.Type))
			}
			continue
		}

		if !exists {
			continue
		}
		dataCols = append(dataCols, escapeIdent(f.Name))
		if trimmed == "" || strings.EqualFold(trimmed, "null") {
			dataVals = append(dataVals, "NULL")
		} else {
			dataVals = append(dataVals, formatSQLValue(trimmed, f.Type))
		}
	}

	if len(dataCols) == 0 {
		return "", errors.New("no data columns provided for TDengine INSERT")
	}

	if isStable && len(tagCols) > 0 {
		childTable := strings.TrimSpace(valMap["tbname"])
		if childTable == "" {
			suffix := strings.Join(tagSlugParts, "_")
			if suffix == "" {
				suffix = "auto"
			}
			childTable = storageUnit + "_" + suffix
		}
		return fmt.Sprintf(
			"INSERT INTO %s USING %s (%s) TAGS (%s) (%s) VALUES (%s)",
			qualifyTable(dbName, childTable),
			qualifyTable(dbName, storageUnit),
			strings.Join(tagCols, ", "),
			strings.Join(tagVals, ", "),
			strings.Join(dataCols, ", "),
			strings.Join(dataVals, ", "),
		), nil
	}

	return fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		qualifyTable(dbName, storageUnit),
		strings.Join(dataCols, ", "),
		strings.Join(dataVals, ", "),
	), nil
}

func sanitizeTableIdentPart(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('_')
		}
		if b.Len() >= 24 {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}

func init() {
	engine.RegisterPlugin(NewTDenginePlugin())
}

// NewTDenginePlugin constructs and returns the TDengine database plugin.
func NewTDenginePlugin() *engine.Plugin {
	return &engine.Plugin{
		Type:            engine.DatabaseType_TDengine,
		PluginFunctions: &TDenginePlugin{},
	}
}
