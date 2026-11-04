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

package openobserve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/query"
)

func TestLiveOpenObserve(t *testing.T) {
	endpoint := os.Getenv("WHODB_TEST_OPENOBSERVE_URL")
	if endpoint == "" {
		t.Skip("set WHODB_TEST_OPENOBSERVE_URL to run against a disposable OpenObserve instance")
	}
	config := engine.NewPluginConfig(&engine.Credentials{Hostname: endpoint, Database: "default", Username: "whodb-test@example.com", Password: "whodb-test-local"})
	p := NewOpenObservePlugin().PluginFunctions
	if !p.IsAvailable(context.Background(), config) {
		t.Fatal("OpenObserve unavailable")
	}
	name := fmt.Sprintf("whodb_test_%d", time.Now().UnixNano())
	var result map[string]any
	if err := request(context.Background(), config, http.MethodPost, []string{"default", name, "_json"}, []map[string]any{
		{"message": "hello", "id": 1},
		{"message": "x' OR 1=1;--", "id": 2},
	}, &result); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := request(context.Background(), config, http.MethodDelete, []string{"default", "streams", name}, nil, &result); err != nil {
			t.Error(err)
		}
	})
	// Ingestion becomes searchable asynchronously.
	deadline := time.Now().Add(30 * time.Second)
	for {
		count, err := p.GetRowCount(config, "", name, nil)
		if err == nil && count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ingested data unavailable: count=%d, error=%v", count, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	filter := &query.WhereCondition{Type: query.WhereConditionTypeAtomic, Atomic: &query.AtomicWhereCondition{Key: "message", Operator: "=", Value: "x' OR 1=1;--"}}
	rows, err := p.GetRows(config, &engine.GetRowsRequest{StorageUnit: name, PageSize: 10, Where: filter})
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("filtered rows: %+v, %v", rows, err)
	}
	count, err := p.GetRowCount(config, "", name, filter)
	if err != nil || count != 1 {
		t.Fatalf("filtered count: %d, %v", count, err)
	}
	rows, err = p.GetRows(config, &engine.GetRowsRequest{StorageUnit: name, PageSize: 1, PageOffset: 1, Sort: []*query.SortCondition{{Column: "id", Direction: query.SortDirectionAsc}}})
	if err != nil || len(rows.Rows) != 1 || rows.TotalCount != 2 {
		t.Fatalf("paginated rows: %+v, %v", rows, err)
	}
	rows, err = p.RawExecute(config, `SELECT count(*) AS total FROM "`+name+`"`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "2" {
		t.Fatalf("raw SQL: %+v, %v", rows, err)
	}
	written := 0
	if err := p.ExportData(config, "", name, func(_ []string) error { written++; return nil }, nil); err != nil || written != 3 {
		t.Fatalf("export: %d rows, %v", written, err)
	}
}

func TestLogStreamOperations(t *testing.T) {
	var searches int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user@example.com" || password != "secret" {
			t.Error("missing basic authentication")
		}
		if r.URL.Query().Get("type") != "logs" {
			t.Error("missing log stream type")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prefix/api/organizations":
			_, _ = w.Write([]byte(`{"data":[{"identifier":"default"}]}`))
		case "/prefix/api/default/streams":
			_, _ = w.Write([]byte(`{"list":[{"name":"app"}]}`))
		case "/prefix/api/default/streams/app/schema":
			_, _ = w.Write([]byte(`{"schema":[{"name":"_timestamp","type":"Int64"},{"name":"message","type":"Utf8"},{"name":"id","type":"UInt64"}]}`))
		case "/prefix/api/default/_search":
			if r.Method != http.MethodPost {
				t.Error("expected POST")
			}
			var body struct {
				Query struct {
					SQL   string `json:"sql"`
					Start int64  `json:"start_time"`
					End   int64  `json:"end_time"`
					From  int    `json:"from"`
					Size  int    `json:"size"`
					Track bool   `json:"track_total_hits"`
				} `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Query.End-body.Query.Start != int64(2*time.Hour/time.Microsecond) || body.Query.Track {
				t.Error("incorrect search window or count flag")
			}
			if strings.Contains(body.Query.SQL, "count(*)") {
				_, _ = w.Write([]byte(`{"hits":[{"count":42}],"total":1}`))
			} else {
				if searches == 0 && (body.Query.From != 10 || body.Query.Size != 5) {
					t.Error("incorrect pagination")
				}
				_, _ = w.Write([]byte(`{"hits":[{"_timestamp":1700000000000000,"message":"hello","id":18446744073709551615}],"total":42}`))
			}
			searches++
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	config := engine.NewPluginConfig(&engine.Credentials{Hostname: server.URL + "/prefix", Database: "default", Username: "user@example.com", Password: "secret", Advanced: []engine.Record{{Key: "Query Lookback Hours", Value: "2"}}})
	p := NewOpenObservePlugin().PluginFunctions
	if !p.IsAvailable(context.Background(), config) {
		t.Fatal("connection failed")
	}
	databases, err := p.GetDatabases(config)
	if err != nil || len(databases) != 1 || databases[0] != "default" {
		t.Fatalf("organizations: %v, %v", databases, err)
	}
	exists, err := p.StorageUnitExists(config, "", "app")
	if err != nil || !exists {
		t.Fatalf("stream existence: %v, %v", exists, err)
	}
	rows, err := p.GetRows(config, &engine.GetRowsRequest{StorageUnit: "app", PageSize: 5, PageOffset: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Columns) != 3 || len(rows.Rows) != 1 || rows.Rows[0][2] != "18446744073709551615" || rows.TotalCount != 42 || !rows.DisableUpdate {
		t.Fatalf("incorrect rows: %+v", rows)
	}
	count, err := p.GetRowCount(config, "", "app", nil)
	if err != nil || count != 42 {
		t.Fatalf("count: %d, %v", count, err)
	}
	if _, err := p.RawExecute(config, `SELECT * FROM "app"`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AddRow(config, "", "app", nil); err == nil {
		t.Fatal("writes should be unsupported")
	}
}

func TestBuildQueryEscapesIdentifiersAndValues(t *testing.T) {
	name := `logs"; DROP TABLE guard;--`
	key := `message"; DROP TABLE guard;--`
	value := `x' OR 1=1;--`
	sql, err := buildQuery(&engine.GetRowsRequest{StorageUnit: name, Where: &query.WhereCondition{
		Type:   query.WhereConditionTypeAtomic,
		Atomic: &query.AtomicWhereCondition{Key: key, Operator: "=", Value: value},
	}, Sort: []*query.SortCondition{{Column: key, Direction: query.SortDirectionAsc}}}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `"logs""; DROP TABLE guard;--"`) || !strings.Contains(sql, `"message""; DROP TABLE guard;--" = 'x'' OR 1=1;--'`) {
		t.Fatalf("unsafe SQL: %s", sql)
	}
	_, err = buildQuery(&engine.GetRowsRequest{StorageUnit: "app", Where: &query.WhereCondition{Type: query.WhereConditionTypeAtomic, Atomic: &query.AtomicWhereCondition{Key: "id", Operator: "= 1; DROP TABLE guard;--"}}}, nil, false)
	if err == nil {
		t.Fatal("operator injection was accepted")
	}
	_, err = buildQuery(&engine.GetRowsRequest{StorageUnit: "app", Sort: []*query.SortCondition{{Column: "id", Direction: "ASC; DROP TABLE guard;--"}}}, nil, false)
	if err == nil {
		t.Fatal("sort injection was accepted")
	}
}

func TestEmptyStreamsRetainSchema(t *testing.T) {
	p := &OpenObservePlugin{}
	rows := p.rows(&searchResponse{}, []engine.Column{{Name: "message", Type: "Utf8"}})
	if len(rows.Columns) != 1 || len(rows.Rows) != 0 {
		t.Fatalf("incorrect empty result: %+v", rows)
	}
}

func TestSearchFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, `password=secret`},
		{"malformed", 200, `{`},
		{"partial", 200, `{"hits":[],"is_partial":true}`},
		{"function error", 200, `{"hits":[],"function_error":["failed"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			config := engine.NewPluginConfig(&engine.Credentials{Hostname: server.URL, Database: "default"})
			_, err := search(config, `SELECT * FROM "app"`, 10, 0)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("expected safe error, got %v", err)
			}
		})
	}
}

func TestCancellationAndInvalidWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := engine.NewPluginConfig(&engine.Credentials{Hostname: "http://localhost:1", Database: "default"})
	config.Context = ctx
	if _, err := search(config, "SELECT 1", 1, 0); err == nil {
		t.Fatal("cancellation ignored")
	}
	for _, value := range []string{"0", "-1", "NaN", "Inf", "invalid"} {
		config.Credentials.Advanced = []engine.Record{{Key: "Query Lookback Hours", Value: value}}
		if _, _, err := queryWindow(config); err == nil {
			t.Fatalf("accepted invalid window: %s", value)
		}
	}
}
