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

package tdengine

import (
	"fmt"
	"strings"

	"github.com/clidey/whodb/core/src/engine"
)

// CopyStorageUnit copies a TDengine supertable or table (structure and optionally data)
// from sourceConfig/sourceSchema to targetConfig/targetSchema.
func (p *TDenginePlugin) CopyStorageUnit(
	sourceConfig *engine.PluginConfig,
	sourceSchema string,
	targetConfig *engine.PluginConfig,
	targetSchema string,
	storageUnit string,
	includeData bool,
	existingStrategy engine.CopyExistingStrategy,
) (*engine.CopyTableDetail, error) {
	sourceClient, err := newTDengineClient(sourceConfig)
	if err != nil {
		return nil, err
	}
	targetClient, err := newTDengineClient(targetConfig)
	if err != nil {
		return nil, err
	}

	sourceDB := resolveDatabaseName(sourceSchema, sourceConfig)
	targetDB := resolveDatabaseName(targetSchema, targetConfig)
	if sourceDB == "" {
		return nil, fmt.Errorf("source TDengine database is required")
	}
	if targetDB == "" {
		return nil, fmt.Errorf("target TDengine database is required")
	}

	sourceExists, err := p.StorageUnitExists(sourceConfig, sourceDB, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect source table %s: %w", storageUnit, err)
	}
	if !sourceExists {
		return nil, fmt.Errorf("source table %q does not exist", storageUnit)
	}

	ctx := sourceConfig.OperationContext()
	targetCtx := targetConfig.OperationContext()
	sourceIsStable := isTDengineSupertable(ctx, sourceClient, sourceDB, storageUnit)

	targetExists, err := p.StorageUnitExists(targetConfig, targetDB, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect target table %s: %w", storageUnit, err)
	}

	if targetExists {
		switch existingStrategy {
		case engine.CopyExistingStrategySkip:
			return &engine.CopyTableDetail{
				Name:       storageUnit,
				Status:     engine.CopyTableStatusSkipped,
				RowsCopied: 0,
				Message:    "Skipped existing table",
			}, nil
		case engine.CopyExistingStrategyOverwrite:
			targetIsStable := isTDengineSupertable(targetCtx, targetClient, targetDB, storageUnit)
			dropKind := "TABLE"
			if targetIsStable {
				dropKind = "STABLE"
			}
			dropSQL := fmt.Sprintf("DROP %s IF EXISTS %s", dropKind, qualifyTable(targetDB, storageUnit))
			if _, err := targetClient.query(targetCtx, targetDB, dropSQL); err != nil {
				return nil, fmt.Errorf("failed to drop existing target %s %s: %w", strings.ToLower(dropKind), storageUnit, err)
			}
			targetExists = false
		case engine.CopyExistingStrategyAppend:
			// Keep existing target table/supertable and append rows below.
		}
	}

	sourceFields, err := describeTDengineTable(ctx, sourceClient, sourceDB, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to describe source table %s: %w", storageUnit, err)
	}

	if !targetExists {
		ddl, ddlErr := p.GetTableSchema(sourceConfig, sourceDB, storageUnit)
		created := false
		if ddlErr == nil && strings.TrimSpace(ddl) != "" {
			if _, execErr := targetClient.query(targetCtx, targetDB, ddl); execErr == nil {
				created = true
			}
		}
		if !created {
			colDefs := make([]engine.ColumnDefinition, 0, len(sourceFields))
			for _, f := range sourceFields {
				if f.IsTag {
					continue
				}
				colDefs = append(colDefs, engine.ColumnDefinition{
					Name:    f.Name,
					Type:    f.Type,
					Primary: f.IsPrimary,
				})
			}
			ok, createErr := p.CreateStorageUnit(targetConfig, targetDB, engine.ObjectDefinition{
				Name:    storageUnit,
				Columns: colDefs,
			})
			if createErr != nil {
				return nil, fmt.Errorf("failed to create target table %s: %w", storageUnit, createErr)
			}
			if !ok {
				return nil, fmt.Errorf("failed to create target table %s", storageUnit)
			}
		}
	}

	if !includeData {
		return &engine.CopyTableDetail{
			Name:       storageUnit,
			Status:     engine.CopyTableStatusSuccess,
			RowsCopied: 0,
		}, nil
	}

	targetFields, err := describeTDengineTable(targetCtx, targetClient, targetDB, storageUnit)
	if err != nil {
		targetFields = sourceFields
	}

	const batchSize = 500
	totalCopied := 0
	selectPrefix := "SELECT *"
	if sourceIsStable {
		selectPrefix = "SELECT tbname, *"
	}

	for offset := 0; ; offset += batchSize {
		querySQL := fmt.Sprintf(
			"%s FROM %s LIMIT %d OFFSET %d",
			selectPrefix,
			qualifyTable(sourceDB, storageUnit),
			batchSize,
			offset,
		)
		resp, err := sourceClient.query(ctx, sourceDB, querySQL)
		if err != nil {
			return nil, fmt.Errorf("failed to read rows from %s at offset %d: %w", storageUnit, offset, err)
		}
		if resp == nil || len(resp.Data) == 0 {
			break
		}

		colNames := make([]string, len(resp.ColumnMeta))
		for i, meta := range resp.ColumnMeta {
			if len(meta) > 0 {
				colNames[i] = formatCell(meta[0])
			}
		}

		for _, rawRow := range resp.Data {
			recRow := make([]engine.Record, 0, len(colNames))
			for idx, colName := range colNames {
				if colName == "" || idx >= len(rawRow) || rawRow[idx] == nil {
					continue
				}
				recRow = append(recRow, engine.Record{
					Key:   colName,
					Value: formatCell(rawRow[idx]),
				})
			}
			if len(recRow) == 0 {
				continue
			}
			insertSQL, err := buildTDengineInsertSQL(targetCtx, targetClient, targetDB, storageUnit, targetFields, recRow)
			if err != nil {
				return nil, fmt.Errorf("failed to build TDengine insert for %s: %w", storageUnit, err)
			}
			if _, err := targetClient.query(targetCtx, targetDB, insertSQL); err != nil {
				return nil, fmt.Errorf("failed to insert row into %s: %w", storageUnit, err)
			}
			totalCopied++
		}

		if len(resp.Data) < batchSize {
			break
		}
	}

	return &engine.CopyTableDetail{
		Name:       storageUnit,
		Status:     engine.CopyTableStatusSuccess,
		RowsCopied: totalCopied,
	}, nil
}
