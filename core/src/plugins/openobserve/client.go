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

// Package openobserve implements read-only access to OpenObserve log streams.
package openobserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/clidey/whodb/core/src/dbcatalog"
	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/sourcedial"
)

func request(ctx context.Context, config *engine.PluginConfig, method string, path []string, body any, target any) error {
	host := config.Credentials.Hostname
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid OpenObserve endpoint")
	}
	if u.Port() == "" {
		port, _ := dbcatalog.DefaultPort(engine.DatabaseType_OpenObserve)
		portValue := strconv.Itoa(port)
		for _, field := range config.Credentials.Advanced {
			if field.Key == "Port" && field.Value != "" {
				portValue = field.Value
			}
		}
		// Explicit HTTPS URLs use the standard HTTPS port unless overridden.
		if u.Scheme != "https" || portValue != strconv.Itoa(port) {
			u.Host = net.JoinHostPort(u.Hostname(), portValue)
		}
	}
	u = u.JoinPath(append([]string{"api"}, path...)...)
	q := u.Query()
	q.Set("type", "logs")
	u.RawQuery = q.Encode()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), &payload)
	if err != nil {
		return err
	}
	req.SetBasicAuth(config.Credentials.Username, config.Credentials.Password)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	if dial := sourcedial.Dialer(); dial != nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = dial
		transport.Proxy = nil
		client.Transport = transport
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("OpenObserve request failed (HTTP %d)", res.StatusCode)
	}
	decoder := json.NewDecoder(res.Body)
	decoder.UseNumber()
	return decoder.Decode(target)
}

func queryWindow(config *engine.PluginConfig) (int64, int64, error) {
	hours := 24.0
	for _, field := range config.Credentials.Advanced {
		if field.Key == "Query Lookback Hours" && field.Value != "" {
			var err error
			hours, err = strconv.ParseFloat(field.Value, 64)
			if err != nil || !(hours > 0 && hours <= 24*365*100) {
				return 0, 0, fmt.Errorf("OpenObserve query lookback must be between 0 and 876000 hours")
			}
		}
	}
	end := time.Now().UnixMicro()
	return end - int64(hours*float64(time.Hour/time.Microsecond)), end, nil
}
