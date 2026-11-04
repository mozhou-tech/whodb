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

package gorm_plugin

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/plugins"
	"gorm.io/gorm"
)

// CopyStorageUnit copies a relational table (structure and optionally data) from sourceConfig/sourceSchema
// to targetConfig/targetSchema.
func (p *GormPlugin) CopyStorageUnit(
	sourceConfig *engine.PluginConfig,
	sourceSchema string,
	targetConfig *engine.PluginConfig,
	targetSchema string,
	storageUnit string,
	includeData bool,
	existingStrategy engine.CopyExistingStrategy,
) (*engine.CopyTableDetail, error) {
	sourceExists, err := p.StorageUnitExists(sourceConfig, sourceSchema, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect source table %s: %w", storageUnit, err)
	}
	if !sourceExists {
		return nil, fmt.Errorf("source table %q does not exist", storageUnit)
	}

	columns, err := p.GetColumnsForTable(sourceConfig, sourceSchema, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to get columns for source table %s: %w", storageUnit, err)
	}
	_ = p.MarkGeneratedColumns(sourceConfig, sourceSchema, storageUnit, columns)

	targetExists, err := p.StorageUnitExists(targetConfig, targetSchema, storageUnit)
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
			dropped, dropErr := plugins.WithConnection(targetConfig, p.DB, func(db *gorm.DB) (bool, error) {
				builder := NewSQLBuilder(db, p.GormPluginFunctions)
				fullTable, err := builder.QualifiedTableName(targetSchema, storageUnit)
				if err != nil {
					return false, err
				}
				if err := db.Exec("DROP TABLE IF EXISTS " + fullTable).Error; err != nil {
					return false, err
				}
				return true, nil
			})
			if dropErr == nil && dropped {
				targetExists = false
			} else {
				if _, clearErr := p.ClearTableData(targetConfig, targetSchema, storageUnit); clearErr != nil {
					return nil, fmt.Errorf("failed to overwrite target table %s: %w", storageUnit, clearErr)
				}
			}
		case engine.CopyExistingStrategyAppend:
			// Keep existing target table and append rows below.
		}
	}

	if !targetExists {
		records := make([]engine.Record, 0, len(columns))
		for _, col := range columns {
			if col.IsComputed {
				continue
			}
			colType := strings.TrimSpace(col.Type)
			upperType := strings.ToUpper(colType)
			if upperType == "VARCHAR" || upperType == "CHAR" {
				colType = colType + "(255)"
			}
			records = append(records, engine.Record{
				Key:   col.Name,
				Value: colType,
				Extra: map[string]string{
					"primary":  strconv.FormatBool(col.IsPrimary),
					"nullable": strconv.FormatBool(col.IsNullable),
				},
			})
		}

		if len(records) == 0 {
			return nil, fmt.Errorf("source table %s has no copyable columns", storageUnit)
		}

		_, err := plugins.WithConnection(targetConfig, p.DB, func(db *gorm.DB) (bool, error) {
			createSQL := p.GormPluginFunctions.GetCreateTableQuery(db, targetSchema, storageUnit, records)
			if err := db.Exec(createSQL).Error; err != nil {
				return false, err
			}
			return true, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create target table %s: %w", storageUnit, err)
		}
	}

	if !includeData {
		return &engine.CopyTableDetail{
			Name:       storageUnit,
			Status:     engine.CopyTableStatusSuccess,
			RowsCopied: 0,
		}, nil
	}

	rowsCopied, err := engine.CopyRowsBatched(
		p,
		sourceConfig,
		sourceSchema,
		targetConfig,
		targetSchema,
		storageUnit,
		columns,
		existingStrategy,
	)
	if err != nil {
		return nil, err
	}

	return &engine.CopyTableDetail{
		Name:       storageUnit,
		Status:     engine.CopyTableStatusSuccess,
		RowsCopied: rowsCopied,
	}, nil
}
