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
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/clidey/whodb/core/src/common"
	"github.com/clidey/whodb/core/src/common/ssl"
	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/sourcedial"
)

const (
	defaultTDenginePort = 6041
	portKey             = "Port"
)

type tdengineClient struct {
	baseURL    string
	authHeader string
	database   string
	httpClient *http.Client
}

type tdengineResponse struct {
	Code       int     `json:"code"`
	Desc       string  `json:"desc"`
	ColumnMeta [][]any `json:"column_meta"`
	Data       [][]any `json:"data"`
	Rows       int     `json:"rows"`
}

func newTDengineClient(config *engine.PluginConfig) (*tdengineClient, error) {
	if config == nil || config.Credentials == nil {
		return nil, errors.New("missing TDengine credentials")
	}

	creds := config.Credentials
	rawHost := strings.TrimSpace(creds.Hostname)
	if rawHost == "" {
		return nil, errors.New("TDengine hostname is required")
	}

	scheme := "http"
	host := rawHost
	port := defaultTDenginePort
	database := strings.TrimSpace(creds.Database)

	if parsedScheme, parsedHost, parsedPort, parsedDB, ok := parseTDengineEndpoint(rawHost); ok {
		if parsedScheme != "" {
			scheme = parsedScheme
		}
		host = parsedHost
		if parsedPort > 0 {
			port = parsedPort
		}
		if database == "" && parsedDB != "" {
			database = parsedDB
		}
	}

	if rawPort := strings.TrimSpace(common.GetRecordValueOrDefault(creds.Advanced, portKey, "")); rawPort != "" {
		parsedPort, err := strconv.Atoi(rawPort)
		if err != nil || parsedPort < 1 || parsedPort > math.MaxUint16 {
			return nil, fmt.Errorf("invalid TDengine port %q", rawPort)
		}
		port = parsedPort
	}

	sslConfig := ssl.ParseSSLConfig(engine.DatabaseType_TDengine, creds.Advanced, host, creds.IsProfile)
	var tlsConfig *tls.Config
	if sslConfig != nil && sslConfig.IsEnabled() {
		scheme = "https"
		var err error
		tlsConfig, err = ssl.BuildTLSConfig(sslConfig, host)
		if err != nil {
			return nil, err
		}
	}

	transport := &http.Transport{
		DialContext:           sourcedial.Dialer(),
		TLSClientConfig:       tlsConfig,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
	}

	username := creds.Username
	if username == "" {
		username = "root"
	}
	token := base64.StdEncoding.EncodeToString([]byte(username + ":" + creds.Password))

	return &tdengineClient{
		baseURL:    fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, strconv.Itoa(port))),
		authHeader: "Basic " + token,
		database:   database,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}, nil
}

func parseTDengineEndpoint(raw string) (scheme string, host string, port int, database string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	for _, prefix := range []string{"jdbc:TAOS-WS://", "jdbc:TAOS-RS://", "jdbc:TAOS://", "jdbc:taos-ws://", "jdbc:taos-rs://", "jdbc:taos://"} {
		if strings.HasPrefix(trimmed, prefix) {
			trimmed = "http://" + strings.TrimPrefix(trimmed, prefix)
			break
		}
	}
	if !strings.Contains(trimmed, "://") {
		if h, p, err := net.SplitHostPort(trimmed); err == nil && h != "" {
			if parsedPort, err := strconv.Atoi(p); err == nil && parsedPort > 0 && parsedPort <= math.MaxUint16 {
				return "", h, parsedPort, "", true
			}
		}
		return "", "", 0, "", false
	}

	u, err := url.Parse(trimmed)
	if err != nil || u.Hostname() == "" {
		return "", "", 0, "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		scheme = "https"
	default:
		scheme = "http"
	}
	if p := u.Port(); p != "" {
		if parsedPort, err := strconv.Atoi(p); err == nil && parsedPort > 0 && parsedPort <= math.MaxUint16 {
			port = parsedPort
		}
	}
	database = strings.Trim(u.Path, "/")
	return scheme, u.Hostname(), port, database, true
}

func (c *tdengineClient) query(ctx context.Context, database string, sqlText string) (*tdengineResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	dbName := strings.TrimSpace(database)
	if dbName == "" {
		dbName = c.database
	}

	endpoint := c.baseURL + "/rest/sql"
	if dbName != "" {
		endpoint += "/" + url.PathEscape(dbName)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(sqlText))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed tdengineResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		if resp.StatusCode >= http.StatusBadRequest {
			return nil, fmt.Errorf("TDengine HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("failed to decode TDengine response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest || parsed.Code != 0 {
		msg := strings.TrimSpace(parsed.Desc)
		if msg == "" {
			msg = fmt.Sprintf("TDengine error code %d (HTTP %d)", parsed.Code, resp.StatusCode)
		}
		return nil, errors.New(msg)
	}

	return &parsed, nil
}
