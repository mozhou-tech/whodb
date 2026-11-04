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

package engine

import (
	"fmt"
	"strconv"
)

// CopyExistingStrategy controls how an existing destination storage unit is handled during copy.
type CopyExistingStrategy string

const (
	// CopyExistingStrategySkip skips copying when the target storage unit already exists.
	CopyExistingStrategySkip CopyExistingStrategy = "SKIP"
	// CopyExistingStrategyOverwrite recreates or clears the target storage unit before copying.
	CopyExistingStrategyOverwrite CopyExistingStrategy = "OVERWRITE"
	// CopyExistingStrategyAppend keeps the existing target storage unit and appends rows.
	CopyExistingStrategyAppend CopyExistingStrategy = "APPEND"
)

// CopyTableStatus represents the outcome of copying a single storage unit.
type CopyTableStatus string

const (
	// CopyTableStatusSuccess indicates the storage unit was copied successfully.
	CopyTableStatusSuccess CopyTableStatus = "SUCCESS"
	// CopyTableStatusSkipped indicates the storage unit was skipped because it already existed.
	CopyTableStatusSkipped CopyTableStatus = "SKIPPED"
	// CopyTableStatusFailed indicates copying the storage unit failed.
	CopyTableStatusFailed CopyTableStatus = "FAILED"
)

// CopyTableDetail holds the execution result for one copied storage unit.
type CopyTableDetail struct {
	Name       string
	Status     CopyTableStatus
	RowsCopied int
	Message    string
}

// StorageUnitCopier is implemented by plugins that provide specialized logic
// for copying a storage unit (structure and optionally data) to a target connection.
type StorageUnitCopier interface {
	CopyStorageUnit(
		sourceConfig *PluginConfig,
		sourceSchema string,
		targetConfig *PluginConfig,
		targetSchema string,
		storageUnit string,
		includeData bool,
		existingStrategy CopyExistingStrategy,
	) (*CopyTableDetail, error)
}

// CopyStorageUnit executes a storage unit copy using the plugin's StorageUnitCopier
// implementation when available, falling back to generic PluginFunctions primitives.
func CopyStorageUnit(
	plugin PluginFunctions,
	sourceConfig *PluginConfig,
	sourceSchema string,
	targetConfig *PluginConfig,
	targetSchema string,
	storageUnit string,
	includeData bool,
	existingStrategy CopyExistingStrategy,
) (*CopyTableDetail, error) {
	if copier, ok := plugin.(StorageUnitCopier); ok {
		return copier.CopyStorageUnit(
			sourceConfig,
			sourceSchema,
			targetConfig,
			targetSchema,
			storageUnit,
			includeData,
			existingStrategy,
		)
	}
	return copyStorageUnitGeneric(
		plugin,
		sourceConfig,
		sourceSchema,
		targetConfig,
		targetSchema,
		storageUnit,
		includeData,
		existingStrategy,
	)
}

func copyStorageUnitGeneric(
	plugin PluginFunctions,
	sourceConfig *PluginConfig,
	sourceSchema string,
	targetConfig *PluginConfig,
	targetSchema string,
	storageUnit string,
	includeData bool,
	existingStrategy CopyExistingStrategy,
) (*CopyTableDetail, error) {
	sourceExists, err := plugin.StorageUnitExists(sourceConfig, sourceSchema, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect source object %s: %w", storageUnit, err)
	}
	if !sourceExists {
		return nil, fmt.Errorf("source object %q does not exist", storageUnit)
	}

	targetExists, err := plugin.StorageUnitExists(targetConfig, targetSchema, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect target object %s: %w", storageUnit, err)
	}

	if targetExists {
		switch existingStrategy {
		case CopyExistingStrategySkip:
			return &CopyTableDetail{
				Name:       storageUnit,
				Status:     CopyTableStatusSkipped,
				RowsCopied: 0,
				Message:    "Skipped existing object",
			}, nil
		case CopyExistingStrategyOverwrite:
			if _, err := plugin.ClearTableData(targetConfig, targetSchema, storageUnit); err != nil {
				return nil, fmt.Errorf("failed to clear target object %s: %w", storageUnit, err)
			}
		case CopyExistingStrategyAppend:
			// Keep existing target object and append rows below.
		}
	}

	columns, err := plugin.GetColumnsForTable(sourceConfig, sourceSchema, storageUnit)
	if err != nil {
		columns = nil
	} else {
		_ = plugin.MarkGeneratedColumns(sourceConfig, sourceSchema, storageUnit, columns)
	}

	if !targetExists {
		colDefs := make([]ColumnDefinition, 0, len(columns))
		for _, col := range columns {
			if col.IsComputed {
				continue
			}
			nullable := col.IsNullable
			colDefs = append(colDefs, ColumnDefinition{
				Name:     col.Name,
				Type:     col.Type,
				Primary:  col.IsPrimary,
				Nullable: &nullable,
			})
		}
		created, err := plugin.CreateStorageUnit(targetConfig, targetSchema, ObjectDefinition{
			Name:    storageUnit,
			Columns: colDefs,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create target object %s: %w", storageUnit, err)
		}
		if !created {
			return nil, fmt.Errorf("failed to create target object %s", storageUnit)
		}
	}

	if !includeData {
		return &CopyTableDetail{
			Name:       storageUnit,
			Status:     CopyTableStatusSuccess,
			RowsCopied: 0,
		}, nil
	}

	rowsCopied, err := CopyRowsBatched(plugin, sourceConfig, sourceSchema, targetConfig, targetSchema, storageUnit, columns, existingStrategy)
	if err != nil {
		return nil, err
	}

	return &CopyTableDetail{
		Name:       storageUnit,
		Status:     CopyTableStatusSuccess,
		RowsCopied: rowsCopied,
	}, nil
}

// CopyRowsBatched reads rows from the source storage unit in batches and inserts them into the target storage unit.
func CopyRowsBatched(
	plugin PluginFunctions,
	sourceConfig *PluginConfig,
	sourceSchema string,
	targetConfig *PluginConfig,
	targetSchema string,
	storageUnit string,
	columns []Column,
	existingStrategy CopyExistingStrategy,
) (int, error) {
	const batchSize = 500

	colMap := make(map[string]Column, len(columns))
	var primaryKeys []string
	for _, col := range columns {
		colMap[col.Name] = col
		if col.IsPrimary {
			primaryKeys = append(primaryKeys, col.Name)
		}
	}

	insertConfig := *targetConfig
	if existingStrategy == CopyExistingStrategyAppend && len(primaryKeys) > 0 {
		insertConfig.SkipConflictPKColumns = primaryKeys
	}

	totalCopied := 0
	for offset := 0; ; offset += batchSize {
		res, err := plugin.GetRows(sourceConfig, &GetRowsRequest{
			Schema:      sourceSchema,
			StorageUnit: storageUnit,
			PageSize:    batchSize,
			PageOffset:  offset,
		})
		if err != nil {
			return totalCopied, fmt.Errorf("failed to read rows from %s at offset %d: %w", storageUnit, offset, err)
		}
		if res == nil || len(res.Rows) == 0 {
			break
		}

		rowCols := res.Columns
		if len(rowCols) == 0 {
			rowCols = columns
		}

		records := make([][]Record, 0, len(res.Rows))
		for _, row := range res.Rows {
			recordRow := make([]Record, 0, len(rowCols))
			for idx, col := range rowCols {
				meta, hasMeta := colMap[col.Name]
				if hasMeta && meta.IsComputed {
					continue
				}
				val := ""
				if idx < len(row) {
					val = row[idx]
				}
				colType := col.Type
				isNullable := col.IsNullable
				if hasMeta {
					if meta.Type != "" {
						colType = meta.Type
					}
					isNullable = meta.IsNullable
				}
				extra := map[string]string{
					"Type":       colType,
					"IsNullable": strconv.FormatBool(isNullable),
				}
				recordRow = append(recordRow, Record{
					Key:   col.Name,
					Value: val,
					Extra: extra,
				})
			}
			if len(recordRow) > 0 {
				records = append(records, recordRow)
			}
		}

		if len(records) > 0 {
			ok, err := plugin.BulkAddRows(&insertConfig, targetSchema, storageUnit, records)
			if err != nil {
				return totalCopied, fmt.Errorf("failed to insert rows into %s: %w", storageUnit, err)
			}
			if !ok {
				return totalCopied, fmt.Errorf("failed to insert rows into %s", storageUnit)
			}
			totalCopied += len(records)
		}

		if len(res.Rows) < batchSize {
			break
		}
	}

	return totalCopied, nil
}
