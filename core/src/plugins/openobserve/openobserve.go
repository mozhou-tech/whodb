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
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/query"
)

// OpenObservePlugin reads log stream metadata and executes search queries.
type OpenObservePlugin struct {
	engine.BasePlugin
}

type stream struct {
	Name   string `json:"name"`
	Schema []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"schema"`
}

func init() {
	engine.RegisterPlugin(NewOpenObservePlugin())
}

// NewOpenObservePlugin creates the OpenObserve log stream connector.
func NewOpenObservePlugin() *engine.Plugin {
	return &engine.Plugin{Type: engine.DatabaseType_OpenObserve, PluginFunctions: &OpenObservePlugin{}}
}

// IsAvailable verifies authenticated access to the selected organization's streams.
func (p *OpenObservePlugin) IsAvailable(ctx context.Context, config *engine.PluginConfig) bool {
	var response struct {
		List []stream `json:"list"`
	}
	if _, _, err := queryWindow(config); err != nil {
		return false
	}
	return request(ctx, config, http.MethodGet, []string{config.Credentials.Database, "streams"}, nil, &response) == nil
}

// GetDatabases lists organization identifiers available to the authenticated user.
func (p *OpenObservePlugin) GetDatabases(config *engine.PluginConfig) ([]string, error) {
	var response struct {
		Data []struct {
			Identifier string `json:"identifier"`
		} `json:"data"`
	}
	if err := request(config.OperationContext(), config, http.MethodGet, []string{"organizations"}, nil, &response); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(response.Data))
	for _, org := range response.Data {
		names = append(names, org.Identifier)
	}
	sort.Strings(names)
	return names, nil
}

// GetStorageUnits lists log streams in the selected organization.
func (p *OpenObservePlugin) GetStorageUnits(config *engine.PluginConfig, _ string) ([]engine.StorageUnit, error) {
	var response struct {
		List []stream `json:"list"`
	}
	if err := request(config.OperationContext(), config, http.MethodGet, []string{config.Credentials.Database, "streams"}, nil, &response); err != nil {
		return nil, err
	}
	units := make([]engine.StorageUnit, 0, len(response.List))
	for _, item := range response.List {
		units = append(units, engine.StorageUnit{Name: item.Name})
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Name < units[j].Name })
	return units, nil
}

// StorageUnitExists reports whether a log stream is visible in the organization.
func (p *OpenObservePlugin) StorageUnitExists(config *engine.PluginConfig, schema, name string) (bool, error) {
	units, err := p.GetStorageUnits(config, schema)
	if err != nil {
		return false, err
	}
	for _, unit := range units {
		if unit.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// GetColumnsForTable reads the stream's declared schema, including empty streams.
func (p *OpenObservePlugin) GetColumnsForTable(config *engine.PluginConfig, _ string, name string) ([]engine.Column, error) {
	var response stream
	if err := request(config.OperationContext(), config, http.MethodGet, []string{config.Credentials.Database, "streams", name, "schema"}, nil, &response); err != nil {
		return nil, err
	}
	columns := make([]engine.Column, 0, len(response.Schema))
	for _, field := range response.Schema {
		columns = append(columns, engine.Column{Name: field.Name, Type: field.Type, IsNullable: true})
	}
	return columns, nil
}

type searchResponse struct {
	Hits          []map[string]any `json:"hits"`
	Total         int64            `json:"total"`
	Partial       bool             `json:"is_partial"`
	FunctionError json.RawMessage  `json:"function_error"`
}

func search(config *engine.PluginConfig, sql string, size, offset int) (*searchResponse, error) {
	start, end, err := queryWindow(config)
	if err != nil {
		return nil, err
	}
	return searchWindow(config, sql, size, offset, start, end)
}

func searchWindow(config *engine.PluginConfig, sql string, size, offset int, start, end int64) (*searchResponse, error) {
	// track_total_hits rewrites SQL into a count query rather than adding totals.
	// Keep it disabled; the browser counts separately using the same time window.
	body := map[string]any{"query": map[string]any{
		"sql": sql, "start_time": start, "end_time": end,
		"from": offset, "size": size, "track_total_hits": false,
	}}
	var response searchResponse
	if err := request(config.OperationContext(), config, http.MethodPost, []string{config.Credentials.Database, "_search"}, body, &response); err != nil {
		return nil, err
	}
	if response.Partial {
		return nil, errors.New("OpenObserve returned incomplete search results")
	}
	// OpenObserve versions encode function_error as either a string or an array.
	if value := string(response.FunctionError); value != "" && value != "null" && value != `""` && value != "[]" {
		return nil, errors.New("OpenObserve search reported a function error")
	}
	return &response, nil
}

// ExportData writes selected records or all records in the configured time window.
func (p *OpenObservePlugin) ExportData(config *engine.PluginConfig, schema, name string, writer func([]string) error, selectedRows []map[string]any) error {
	columns, err := p.GetColumnsForTable(config, schema, name)
	if err != nil {
		return err
	}
	headers := make([]string, len(columns))
	for i, column := range columns {
		headers[i] = column.Name
	}
	if err := writer(headers); err != nil {
		return err
	}
	writeRows := func(response *searchResponse) error {
		for _, row := range p.rows(response, columns).Rows {
			if err := writer(row); err != nil {
				return err
			}
		}
		return nil
	}
	if len(selectedRows) > 0 {
		return writeRows(&searchResponse{Hits: selectedRows})
	}
	sql, err := buildQuery(&engine.GetRowsRequest{StorageUnit: name}, columns, false)
	if err != nil {
		return err
	}
	start, end, err := queryWindow(config)
	if err != nil {
		return err
	}
	const pageSize = 1000
	for offset := 0; ; offset += pageSize {
		response, err := searchWindow(config, sql, pageSize, offset, start, end)
		if err != nil {
			return err
		}
		if err := writeRows(response); err != nil {
			return err
		}
		if len(response.Hits) < pageSize {
			return nil
		}
	}
}

func (p *OpenObservePlugin) rows(response *searchResponse, columns []engine.Column) *engine.GetRowsResult {
	if columns == nil {
		keys := map[string]bool{}
		for _, hit := range response.Hits {
			for key := range hit {
				keys[key] = true
			}
		}
		names := make([]string, 0, len(keys))
		for key := range keys {
			names = append(names, key)
		}
		sort.Strings(names)
		columns = make([]engine.Column, 0, len(names))
		for _, name := range names {
			columns = append(columns, engine.Column{Name: name, Type: "Utf8", IsNullable: true})
		}
	}
	rows := make([][]string, 0, len(response.Hits))
	for _, hit := range response.Hits {
		row := make([]string, len(columns))
		for i, col := range columns {
			row[i] = p.FormatValue(hit[col.Name])
		}
		rows = append(rows, row)
	}
	return &engine.GetRowsResult{Columns: columns, Rows: rows, TotalCount: response.Total, DisableUpdate: true}
}

// GetRows reads a page of log records with filters and sorting.
func (p *OpenObservePlugin) GetRows(config *engine.PluginConfig, req *engine.GetRowsRequest) (*engine.GetRowsResult, error) {
	columns, err := p.GetColumnsForTable(config, req.Schema, req.StorageUnit)
	if err != nil {
		return nil, err
	}
	sql, err := buildQuery(req, columns, false)
	if err != nil {
		return nil, err
	}
	start, end, err := queryWindow(config)
	if err != nil {
		return nil, err
	}
	response, err := searchWindow(config, sql, req.PageSize, req.PageOffset, start, end)
	if err != nil {
		return nil, err
	}
	response.Total, err = countWindow(config, req, columns, start, end)
	if err != nil {
		return nil, err
	}
	return p.rows(response, columns), nil
}

// GetRowCount counts matching log records within the configured time range.
func (p *OpenObservePlugin) GetRowCount(config *engine.PluginConfig, schema, name string, where *query.WhereCondition) (int64, error) {
	columns, err := p.GetColumnsForTable(config, schema, name)
	if err != nil {
		return 0, err
	}
	start, end, err := queryWindow(config)
	if err != nil {
		return 0, err
	}
	return countWindow(config, &engine.GetRowsRequest{StorageUnit: name, Where: where}, columns, start, end)
}

func countWindow(config *engine.PluginConfig, req *engine.GetRowsRequest, columns []engine.Column, start, end int64) (int64, error) {
	sql, err := buildQuery(req, columns, true)
	if err != nil {
		return 0, err
	}
	response, err := searchWindow(config, sql, 1, 0, start, end)
	if err != nil {
		return 0, err
	}
	if len(response.Hits) != 1 {
		return 0, errors.New("OpenObserve count query returned no result")
	}
	return strconv.ParseInt(fmt.Sprint(response.Hits[0]["count"]), 10, 64)
}

// RawExecute runs SQL through OpenObserve's search-only API.
func (p *OpenObservePlugin) RawExecute(config *engine.PluginConfig, sql string, params ...any) (*engine.GetRowsResult, error) {
	if len(params) != 0 {
		return nil, errors.ErrUnsupported
	}
	response, err := search(config, sql, 100, 0)
	if err != nil {
		return nil, err
	}
	return p.rows(response, nil), nil
}

// FormatValue preserves JSON numbers and serializes nested log values.
func (p *OpenObservePlugin) FormatValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
