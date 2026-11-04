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
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/query"
)

func newMockTDengineServer(t *testing.T) (*httptest.Server, *engine.PluginConfig) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sqlText := strings.TrimSpace(string(body))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case sqlText == "SHOW DATABASES":
			_ = json.NewEncoder(w).Encode(tdengineResponse{
				Code:       0,
				ColumnMeta: [][]any{{"name", "VARCHAR", 64}},
				Data:       [][]any{{"information_schema"}, {"thingsboard"}},
				Rows:       2,
			})
		case strings.HasPrefix(sqlText, "SHOW ") && strings.HasSuffix(sqlText, "STABLES"):
			_ = json.NewEncoder(w).Encode(tdengineResponse{
				Code:       0,
				ColumnMeta: [][]any{{"stable_name", "VARCHAR", 192}},
				Data:       [][]any{{"ts_kv"}},
				Rows:       1,
			})
		case strings.HasPrefix(sqlText, "SHOW ") && strings.HasSuffix(sqlText, "TABLES"):
			_ = json.NewEncoder(w).Encode(tdengineResponse{
				Code:       0,
				ColumnMeta: [][]any{{"table_name", "VARCHAR", 192}, {"db_name", "VARCHAR", 64}, {"create_time", "TIMESTAMP", 8}, {"columns", "INT", 4}, {"stable_name", "VARCHAR", 192}},
				Data:       [][]any{{"tb_child_1", "thingsboard", "2026-09-05 00:00:00", 2, "ts_kv"}},
				Rows:       1,
			})
		case strings.HasPrefix(sqlText, "DESCRIBE "):
			if strings.Contains(sqlText, "missing_table") {
				_ = json.NewEncoder(w).Encode(tdengineResponse{
					Code: 9731,
					Desc: "Table does not exist",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(tdengineResponse{
				Code: 0,
				ColumnMeta: [][]any{
					{"field", "VARCHAR", 64},
					{"type", "VARCHAR", 20},
					{"length", "INT", 4},
					{"note", "VARCHAR", 16},
				},
				Data: [][]any{
					{"ts", "TIMESTAMP", 8, ""},
					{"dbl_v", "DOUBLE", 8, ""},
					{"tenant_id", "VARCHAR", 36, "TAG"},
				},
				Rows: 3,
			})
		case strings.HasPrefix(sqlText, "SELECT COUNT(*)"):
			_ = json.NewEncoder(w).Encode(tdengineResponse{
				Code:       0,
				ColumnMeta: [][]any{{"count(*)", "BIGINT", 8}},
				Data:       [][]any{{float64(42)}},
				Rows:       1,
			})
		case strings.HasPrefix(sqlText, "SELECT * FROM"):
			_ = json.NewEncoder(w).Encode(tdengineResponse{
				Code: 0,
				ColumnMeta: [][]any{
					{"ts", "TIMESTAMP", 8},
					{"dbl_v", "DOUBLE", 8},
					{"tenant_id", "VARCHAR", 36},
				},
				Data: [][]any{
					{"2026-09-05T03:54:41.873Z", 12.5, "tenant-1"},
				},
				Rows: 1,
			})
		case strings.HasPrefix(sqlText, "SHOW CREATE STABLE"):
			_ = json.NewEncoder(w).Encode(tdengineResponse{
				Code:       0,
				ColumnMeta: [][]any{{"stable", "VARCHAR", 64}, {"create_table", "VARCHAR", 1024}},
				Data:       [][]any{{"ts_kv", "CREATE STABLE `ts_kv` (`ts` TIMESTAMP, `dbl_v` DOUBLE) TAGS (`tenant_id` VARCHAR(36))"}},
				Rows:       1,
			})
		default:
			_ = json.NewEncoder(w).Encode(tdengineResponse{
				Code:       0,
				ColumnMeta: [][]any{{"result", "INT", 4}},
				Data:       [][]any{{float64(1)}},
				Rows:       1,
			})
		}
	}))
	t.Cleanup(srv.Close)

	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	cfg := engine.NewPluginConfig(&engine.Credentials{
		Type:     engine.DatabaseType_TDengine,
		Hostname: host,
		Username: "root",
		Password: "secret",
		Database: "thingsboard",
		Advanced: []engine.Record{{Key: "Port", Value: port}},
	})
	return srv, cfg
}

func TestTDenginePluginOperations(t *testing.T) {
	_, cfg := newMockTDengineServer(t)
	plugin := &TDenginePlugin{}

	if !plugin.IsAvailable(context.Background(), cfg) {
		t.Fatal("expected IsAvailable to return true")
	}

	dbs, err := plugin.GetDatabases(cfg)
	if err != nil {
		t.Fatalf("GetDatabases failed: %v", err)
	}
	if len(dbs) != 2 || dbs[1] != "thingsboard" {
		t.Fatalf("unexpected databases: %v", dbs)
	}

	units, err := plugin.GetStorageUnits(cfg, "thingsboard")
	if err != nil {
		t.Fatalf("GetStorageUnits failed: %v", err)
	}
	if len(units) != 2 || units[0].Name != "ts_kv" || units[1].Name != "tb_child_1" {
		t.Fatalf("unexpected storage units: %+v", units)
	}

	exists, err := plugin.StorageUnitExists(cfg, "thingsboard", "ts_kv")
	if err != nil || !exists {
		t.Fatalf("expected ts_kv to exist, got exists=%v err=%v", exists, err)
	}

	missing, err := plugin.StorageUnitExists(cfg, "thingsboard", "missing_table")
	if err != nil || missing {
		t.Fatalf("expected missing_table not to exist, got exists=%v err=%v", missing, err)
	}

	cols, err := plugin.GetColumnsForTable(cfg, "thingsboard", "ts_kv")
	if err != nil {
		t.Fatalf("GetColumnsForTable failed: %v", err)
	}
	if len(cols) != 3 || !cols[0].IsPrimary || cols[2].Length == nil || *cols[2].Length != 36 {
		t.Fatalf("unexpected columns: %+v", cols)
	}

	rows, err := plugin.GetRows(cfg, &engine.GetRowsRequest{
		Schema:      "thingsboard",
		StorageUnit: "ts_kv",
		PageSize:    10,
		PageOffset:  0,
		Where: &query.WhereCondition{
			Type: query.WhereConditionTypeAtomic,
			Atomic: &query.AtomicWhereCondition{
				Key:        "tenant_id",
				Operator:   "=",
				Value:      "tenant-1",
				ColumnType: "VARCHAR",
			},
		},
		Sort: []*query.SortCondition{
			{Column: "ts", Direction: query.SortDirectionDesc},
		},
	})
	if err != nil {
		t.Fatalf("GetRows failed: %v", err)
	}
	if rows.TotalCount != 42 || len(rows.Rows) != 1 || rows.Rows[0][2] != "tenant-1" {
		t.Fatalf("unexpected rows result: %+v", rows)
	}

	ddl, err := plugin.GetTableSchema(cfg, "thingsboard", "ts_kv")
	if err != nil || !strings.Contains(ddl, "CREATE STABLE") {
		t.Fatalf("unexpected DDL: %q err=%v", ddl, err)
	}

	created, err := plugin.CreateStorageUnit(cfg, "thingsboard", engine.ObjectDefinition{
		Name: "metrics_test",
		Columns: []engine.ColumnDefinition{
			{Name: "val", Type: "DOUBLE"},
		},
	})
	if err != nil || !created {
		t.Fatalf("CreateStorageUnit failed: created=%v err=%v", created, err)
	}

	inserted, err := plugin.AddRow(cfg, "thingsboard", "ts_kv", []engine.Record{
		{Key: "ts", Value: "now"},
		{Key: "dbl_v", Value: "99.5"},
		{Key: "tenant_id", Value: "tenant-1"},
	})
	if err != nil || !inserted {
		t.Fatalf("AddRow failed: inserted=%v err=%v", inserted, err)
	}

	deleted, err := plugin.DeleteRow(cfg, "thingsboard", "ts_kv", map[string]string{
		"ts":        "2026-09-05T03:54:41.873Z",
		"dbl_v":     "12.5",
		"tenant_id": "tenant-1",
	})
	if err != nil || !deleted {
		t.Fatalf("DeleteRow failed: deleted=%v err=%v", deleted, err)
	}

	graphUnits, err := plugin.GetGraph(cfg, "thingsboard")
	if err != nil || len(graphUnits) != 2 || len(graphUnits[0].Relations) != 1 || graphUnits[0].Relations[0].Name != "tb_child_1" {
		t.Fatalf("unexpected graph units: %+v err=%v", graphUnits, err)
	}
}

func TestParseTDengineEndpoint(t *testing.T) {
	scheme, host, port, db, ok := parseTDengineEndpoint("jdbc:TAOS-WS://100.64.0.24:6041/thingsboard")
	if !ok || scheme != "http" || host != "100.64.0.24" || port != 6041 || db != "thingsboard" {
		t.Fatalf("unexpected parsed endpoint: scheme=%q host=%q port=%d db=%q ok=%v", scheme, host, port, db, ok)
	}
}
