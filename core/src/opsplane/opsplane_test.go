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

package opsplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAuthenticateViaHTTPEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/login" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"code": 0,
			"message": "",
			"data": {
				"token": "eyJhbGciOiJIUzI1NiJ9.test",
				"expire_at": "2026-11-05T12:00:00Z",
				"user": {
					"id": 2,
					"username": "admin@mozyun.cn",
					"role": "admin",
					"is_active": true,
					"environment_permissions": ["*"],
					"roles": [{"id": 1, "code": "system_admin", "name": "系统管理员", "scope_type": "global", "environment_slugs": []}]
				}
			}
		}`))
	}))
	defer srv.Close()

	status, err := authenticateViaHTTP(context.Background(), srv.URL, "admin@mozyun.cn", "secret")
	if err != nil {
		t.Fatalf("expected HTTP login with code:0 envelope to succeed, got error: %v", err)
	}
	if !status.Authenticated || status.Username != "admin@mozyun.cn" || !status.AllowAllEnvs || status.Token == "" {
		t.Fatalf("unexpected AuthStatus: %+v", status)
	}
}

func TestBuildEnvironmentProfilesAndProxyRouting(t *testing.T) {
	env := environmentRow{
		Slug:      "nj-test",
		Name:      "测试园区环境",
		GroupName: "ddkj",
		ProxyRaw:  []byte(`{"type":"socks5","host":"100.64.0.31","port":31081,"username":"","password":""}`),
		Postgres:  []byte(`{"enabled":true,"host":"100.64.0.24","port":5433,"username":"mozy","password":"","database":""}`),
		MySQL:     []byte(`{"enabled":true,"host":"10.22.184.109","port":3306,"username":"root","password":"pw","database":""}`),
	}
	envVars := map[string]string{
		"PG_DB_PASSWORD":         "pgpass",
		"REDIS_HOST":             "100.64.0.24",
		"REDIS_PORT":             "6380",
		"REDIS_PASSWORD":         "redispass",
		"TDENGINE_URL":           "jdbc:TAOS-WS://100.64.0.24:6041/thingsboard",
		"TDENGINE_USERNAME":      "root",
		"TDENGINE_PASSWORD":      "taospass",
		"CLICKHOUSE_JDBC_URL":    "jdbc:clickhouse://100.64.0.24:30123/default",
		"CLICKHOUSE_USER":        "mozy",
		"CLICKHOUSE_PASSWORD":    "chpass",
		"ELASTICSEARCH_HTTP_URL": "http://100.64.0.24:31768",
		"ELASTICSEARCH_PASSWORD": "espass",
	}
	proxy := parseProxyConfig(env.ProxyRaw)
	profiles := buildEnvironmentProfiles(env, proxy, envVars, nil)

	if len(profiles) != 6 {
		t.Fatalf("expected 6 profiles (Postgres, MySQL, Redis, TDengine, ClickHouse, ElasticSearch), got %d", len(profiles))
	}

	for _, profile := range profiles {
		if profile.ProjectID != env.Slug || profile.ProjectName != env.Name {
			t.Fatalf("expected project metadata on %s, got id=%q name=%q", profile.Type, profile.ProjectID, profile.ProjectName)
		}
	}

	// 100.64.0.0/24 must bypass proxy; 10.22.184.109 must use the environment SOCKS5 proxy.
	if _, bypassed := LookupTargetProxy("100.64.0.24:5433"); bypassed {
		t.Fatal("expected 100.64.0.24:5433 to bypass proxy")
	}
	routedProxy, ok := LookupTargetProxy("10.22.184.109:3306")
	if !ok || routedProxy.Type != "socks5" || routedProxy.Host != "100.64.0.31" || routedProxy.Port != 31081 {
		t.Fatalf("expected 10.22.184.109:3306 to route via socks5 100.64.0.31:31081, got ok=%v proxy=%+v", ok, routedProxy)
	}
}

func TestLiveOpsControlPlaneDiscovery(t *testing.T) {
	if _, err := os.Stat(defaultOpsControlPlaneDir); err != nil {
		t.Skip("ops-control-plane directory not present")
	}

	// 1. Before logging into ops-control-plane, RetrieveProfiles must return empty and Endpoint must be the platform URL.
	SetSessionForTesting(nil)
	t.Cleanup(func() {
		SetSessionForTesting(nil)
	})

	initialStatus := GetAuthStatus()
	if !strings.HasPrefix(initialStatus.Endpoint, "http://") && !strings.HasPrefix(initialStatus.Endpoint, "https://") {
		t.Fatalf("expected platform HTTP(S) URL for Endpoint, got %q", initialStatus.Endpoint)
	}
	if strings.Contains(initialStatus.Endpoint, "ops_control_plane") {
		t.Fatalf("expected Endpoint to be platform URL rather than database DSN, got %q", initialStatus.Endpoint)
	}

	unauthProfiles, err := RetrieveProfiles()
	if err != nil {
		t.Fatalf("RetrieveProfiles (unauthenticated) failed: %v", err)
	}
	if len(unauthProfiles) != 0 {
		t.Fatalf("expected 0 profiles when unauthenticated, got %d", len(unauthProfiles))
	}

	// 2. When logged in as admin (AllowAllEnvs=true), all environments are synced.
	SetSessionForTesting(&AuthStatus{
		Authenticated: true,
		Username:      "admin",
		Role:          "系统管理员",
		AllowAllEnvs:  true,
	})

	profiles, err := RetrieveProfiles()
	if err != nil {
		t.Fatalf("RetrieveProfiles failed: %v", err)
	}
	if len(profiles) == 0 {
		t.Fatal("expected non-empty profiles discovered from ops-control-plane")
	}

	typesSeen := map[string]int{}
	envsSeen := map[string]bool{}
	for _, p := range profiles {
		typesSeen[p.Type]++
		if strings.HasPrefix(p.Alias, "[") {
			if end := strings.Index(p.Alias, "]"); end > 1 {
				envsSeen[p.Alias[1:end]] = true
			}
		}
	}

	for _, wantType := range []string{"Postgres", "MySQL", "Redis", "TDengine", "ClickHouse", "ElasticSearch"} {
		if typesSeen[wantType] == 0 {
			t.Errorf("expected at least one %s profile, got %v", wantType, typesSeen)
		}
	}
	if len(envsSeen) < 7 {
		t.Errorf("expected at least 7 environments discovered, got %d: %v", len(envsSeen), envsSeen)
	}

	// 3. When logged in with scoped environment access, only permitted environments are returned.
	SetSessionForTesting(&AuthStatus{
		Authenticated:   true,
		Username:        "scoped-user",
		Role:            "运维工程师",
		AllowAllEnvs:    false,
		AllowedEnvSlugs: []string{"test-direct"},
	})
	scopedProfiles, err := RetrieveProfiles()
	if err != nil {
		t.Fatalf("RetrieveProfiles (scoped) failed: %v", err)
	}
	if len(scopedProfiles) == 0 || len(scopedProfiles) >= len(profiles) {
		t.Fatalf("expected scoped profile count between 1 and %d, got %d", len(profiles)-1, len(scopedProfiles))
	}
}
