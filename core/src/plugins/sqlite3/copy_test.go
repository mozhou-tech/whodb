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

package sqlite3

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/clidey/whodb/core/src/engine"
)

func createEmptySQLiteFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create sqlite file %s: %v", path, err)
	}
	_ = f.Close()
	return path
}

func TestCopyStorageUnit(t *testing.T) {
	t.Setenv("WHODB_CLI", "true")

	tmpDir := t.TempDir()
	srcPath := createEmptySQLiteFile(t, tmpDir, "source.db")
	dstPath := createEmptySQLiteFile(t, tmpDir, "target.db")

	plugin := NewSqlite3Plugin().PluginFunctions
	srcCfg := engine.NewPluginConfig(&engine.Credentials{
		Type:     string(engine.DatabaseType_Sqlite3),
		Database: srcPath,
	})
	dstCfg := engine.NewPluginConfig(&engine.Credentials{
		Type:     string(engine.DatabaseType_Sqlite3),
		Database: dstPath,
	})

	nullable := false
	created, err := plugin.CreateStorageUnit(srcCfg, "", engine.ObjectDefinition{
		Name: "users",
		Columns: []engine.ColumnDefinition{
			{Name: "id", Type: "INTEGER", Primary: true, Nullable: &nullable},
			{Name: "name", Type: "TEXT", Primary: false, Nullable: &nullable},
		},
	})
	if err != nil || !created {
		t.Fatalf("failed to create source table: created=%v err=%v", created, err)
	}

	for _, row := range [][]engine.Record{
		{{Key: "id", Value: "1", Extra: map[string]string{"Type": "INTEGER"}}, {Key: "name", Value: "Alice", Extra: map[string]string{"Type": "TEXT"}}},
		{{Key: "id", Value: "2", Extra: map[string]string{"Type": "INTEGER"}}, {Key: "name", Value: "Bob", Extra: map[string]string{"Type": "TEXT"}}},
	} {
		if _, err := plugin.AddRow(srcCfg, "", "users", row); err != nil {
			t.Fatalf("failed to seed source row: %v", err)
		}
	}

	// 1. Copy structure and data to empty target
	detail, err := engine.CopyStorageUnit(plugin, srcCfg, "", dstCfg, "", "users", true, engine.CopyExistingStrategySkip)
	if err != nil {
		t.Fatalf("CopyStorageUnit STRUCTURE_AND_DATA failed: %v", err)
	}
	if detail.Status != engine.CopyTableStatusSuccess || detail.RowsCopied != 2 {
		t.Fatalf("unexpected detail: %+v", detail)
	}

	rowsRes, err := plugin.GetRows(dstCfg, &engine.GetRowsRequest{StorageUnit: "users", PageSize: 10})
	if err != nil {
		t.Fatalf("GetRows on target failed: %v", err)
	}
	if len(rowsRes.Rows) != 2 {
		t.Fatalf("expected 2 rows in target, got %d", len(rowsRes.Rows))
	}

	// 2. Copy with SKIP when target already exists
	detail, err = engine.CopyStorageUnit(plugin, srcCfg, "", dstCfg, "", "users", true, engine.CopyExistingStrategySkip)
	if err != nil {
		t.Fatalf("CopyStorageUnit SKIP failed: %v", err)
	}
	if detail.Status != engine.CopyTableStatusSkipped || detail.RowsCopied != 0 {
		t.Fatalf("expected SKIPPED with 0 rows, got %+v", detail)
	}

	// 3. Copy STRUCTURE_ONLY with OVERWRITE
	detail, err = engine.CopyStorageUnit(plugin, srcCfg, "", dstCfg, "", "users", false, engine.CopyExistingStrategyOverwrite)
	if err != nil {
		t.Fatalf("CopyStorageUnit STRUCTURE_ONLY + OVERWRITE failed: %v", err)
	}
	if detail.Status != engine.CopyTableStatusSuccess || detail.RowsCopied != 0 {
		t.Fatalf("unexpected detail for STRUCTURE_ONLY: %+v", detail)
	}
	rowsRes, err = plugin.GetRows(dstCfg, &engine.GetRowsRequest{StorageUnit: "users", PageSize: 10})
	if err != nil {
		t.Fatalf("GetRows after STRUCTURE_ONLY + OVERWRITE failed: %v", err)
	}
	if len(rowsRes.Rows) != 0 {
		t.Fatalf("expected 0 rows after STRUCTURE_ONLY + OVERWRITE, got %d", len(rowsRes.Rows))
	}

	// 4. Copy with APPEND into existing empty table, then APPEND again (with PK conflict skipping)
	detail, err = engine.CopyStorageUnit(plugin, srcCfg, "", dstCfg, "", "users", true, engine.CopyExistingStrategyAppend)
	if err != nil {
		t.Fatalf("CopyStorageUnit APPEND failed: %v", err)
	}
	if detail.Status != engine.CopyTableStatusSuccess || detail.RowsCopied != 2 {
		t.Fatalf("unexpected detail for APPEND: %+v", detail)
	}
}
