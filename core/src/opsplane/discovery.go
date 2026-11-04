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
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/log"
	"github.com/clidey/whodb/core/src/types"
)

const (
	defaultOpsControlPlaneDir = "/Users/mozhou/WorkSpace/mozy/ops-control-plane"
	defaultOpsEnvsDir         = "/Users/mozhou/WorkSpace/mozy/envs"
	profileCacheTTL           = 5 * time.Minute
	staleCacheRetryTTL        = 30 * time.Second
)

var (
	discoverMu     sync.Mutex
	cacheMu        sync.Mutex
	cachedProfiles []types.DatabaseCredentials
	cachedEnvCount int
	cacheExpiresAt time.Time
)

type dbMiddlewareJSON struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     any    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Database any    `json:"database"`
}

type environmentRow struct {
	Slug      string
	Name      string
	GroupName string
	ProxyRaw  []byte
	Postgres  []byte
	MySQL     []byte
	Redis     []byte
	TDengine  []byte
}

// RetrieveProfiles discovers all environments and database credentials managed by ops-control-plane
// for the currently authenticated ops-control-plane user, and registers per-environment proxy routes
// with the opsplane dialer. Returns nil when no ops-control-plane user is logged in.
func RetrieveProfiles() ([]types.DatabaseCredentials, error) {
	session := loadOrGetSession()
	if session == nil || !session.Authenticated {
		return nil, nil
	}

	profiles, _, err := getOrDiscoverProfilesForSession(session)
	if err != nil {
		log.WithError(err).Warn("Failed to discover ops-control-plane database profiles")
		return nil, nil
	}
	return profiles, nil
}

func getOrDiscoverProfilesForSession(session *AuthStatus) ([]types.DatabaseCredentials, int, error) {
	cacheMu.Lock()
	if time.Now().Before(cacheExpiresAt) && len(cachedProfiles) > 0 {
		cloned := slices.Clone(cachedProfiles)
		envCount := cachedEnvCount
		cacheMu.Unlock()
		return cloned, envCount, nil
	}
	cacheMu.Unlock()

	discoverMu.Lock()
	defer discoverMu.Unlock()

	cacheMu.Lock()
	if time.Now().Before(cacheExpiresAt) && len(cachedProfiles) > 0 {
		cloned := slices.Clone(cachedProfiles)
		envCount := cachedEnvCount
		cacheMu.Unlock()
		return cloned, envCount, nil
	}
	staleProfiles := slices.Clone(cachedProfiles)
	staleEnvCount := cachedEnvCount
	cacheMu.Unlock()

	profiles, envCount, err := discoverProfilesForSession(session)
	if err != nil || len(profiles) == 0 {
		if len(staleProfiles) > 0 {
			cacheMu.Lock()
			cacheExpiresAt = time.Now().Add(staleCacheRetryTTL)
			cacheMu.Unlock()
			return staleProfiles, staleEnvCount, nil
		}
		if err != nil {
			return nil, 0, err
		}
		return nil, 0, nil
	}

	cacheMu.Lock()
	cachedProfiles = slices.Clone(profiles)
	cachedEnvCount = envCount
	cacheExpiresAt = time.Now().Add(profileCacheTTL)
	cacheMu.Unlock()

	return profiles, envCount, nil
}

func discoverProfilesForSession(session *AuthStatus) ([]types.DatabaseCredentials, int, error) {
	opsDir := resolveOpsControlPlaneDir()
	var opsDotEnv, secretEnv, baselineVars map[string]string
	var keyring map[string][]byte
	var envsDir string
	if opsDir != "" {
		opsDotEnv = readDotEnvFile(filepath.Join(opsDir, ".env"))
		secretEnv = readDotEnvFile(filepath.Join(opsDir, ".runtime", "project-env.secret.env"))
		keyring = ParseProjectEnvKeyring(secretEnv)

		envsDir = resolveEnvsDir(opsDir, opsDotEnv)
		baselineEnvPath := strings.TrimSpace(opsDotEnv["MIDDLEWARE_ENV_FILE"])
		if baselineEnvPath != "" && !filepath.IsAbs(baselineEnvPath) {
			baselineEnvPath = filepath.Clean(filepath.Join(opsDir, baselineEnvPath))
		}
		if baselineEnvPath == "" && envsDir != "" {
			baselineEnvPath = filepath.Join(envsDir, "100.env")
		}
		baselineVars = readDotEnvFile(baselineEnvPath)
	}
	localEnvsBySlug, localEnvsByIP := loadLocalEnvFiles(envsDir)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	var envs []environmentRow
	projectEnvVars := make(map[string]map[string]string)

	// 1. Try HTTP API discovery via the Ops Control Plane platform URL + token
	if session != nil && session.Token != "" && session.Endpoint != "" {
		if httpEnvs, err := queryCustomerEnvironmentsViaHTTP(ctx, session.Endpoint, session.Token); err == nil && len(httpEnvs) > 0 {
			envs = httpEnvs
			for slug, vars := range queryProjectEnvVarsViaHTTP(ctx, session.Endpoint, session.Token, httpEnvs) {
				projectEnvVars[slug] = vars
			}
		}
	}

	// 2. Supplement or fallback with control-plane DB discovery when available
	customEndpoint := ""
	if session != nil {
		customEndpoint = session.Endpoint
	}
	dsn, _ := resolveAuthDSN(customEndpoint)
	if dsn == "" {
		dsn = resolveOpsDatabaseDSN(opsDotEnv, baselineVars)
	}
	if dsn != "" {
		if db, err := sql.Open("pgx", dsn); err == nil {
			defer func() { _ = db.Close() }()
			if len(envs) == 0 {
				if dbEnvs, err := queryCustomerEnvironments(ctx, db); err == nil {
					envs = dbEnvs
				}
			}
			for slug, vars := range queryProjectEnvVars(ctx, db, keyring) {
				if projectEnvVars[slug] == nil {
					projectEnvVars[slug] = make(map[string]string)
				}
				for k, v := range vars {
					if _, exists := projectEnvVars[slug][k]; !exists {
						projectEnvVars[slug][k] = v
					}
				}
			}
		}
	}

	if len(envs) == 0 {
		return nil, 0, nil
	}

	profiles := make([]types.DatabaseCredentials, 0, len(envs)*4)
	seenIDs := make(map[string]bool)
	matchedEnvCount := 0

	for _, env := range envs {
		if session != nil && !session.AllowAllEnvs && !slices.Contains(session.AllowedEnvSlugs, env.Slug) {
			continue
		}
		matchedEnvCount++
		proxy := parseProxyConfig(env.ProxyRaw)

		mergedVars := make(map[string]string)
		if env.Slug == "test-direct" {
			for k, v := range baselineVars {
				mergedVars[k] = v
			}
		}
		if bySlug, ok := localEnvsBySlug[env.Slug]; ok {
			for k, v := range bySlug {
				mergedVars[k] = v
			}
		}
		if primaryIP := detectPrimaryEnvironmentIP(proxy, env.Postgres, env.MySQL, env.Redis, env.TDengine); primaryIP != "" {
			if byIP, ok := localEnvsByIP[primaryIP]; ok {
				for k, v := range byIP {
					mergedVars[k] = v
				}
			}
		}
		if fromPlatform, ok := projectEnvVars[env.Slug]; ok {
			for k, v := range fromPlatform {
				mergedVars[k] = v
			}
		}

		envProfiles := buildEnvironmentProfiles(env, proxy, mergedVars, baselineVars)
		for _, p := range envProfiles {
			if seenIDs[p.CustomId] {
				continue
			}
			seenIDs[p.CustomId] = true
			profiles = append(profiles, p)
		}
	}

	return profiles, matchedEnvCount, nil
}

func queryCustomerEnvironmentsViaHTTP(ctx context.Context, baseURL string, token string) ([]environmentRow, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/environments"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	resp, err := opsHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, endpoint)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	type apiEnvItem struct {
		Slug       string          `json:"slug"`
		Name       string          `json:"name"`
		Proxy      json.RawMessage `json:"proxy"`
		PostgreSQL json.RawMessage `json:"postgresql"`
		PostgresDB json.RawMessage `json:"postgre_sql"`
		MySQL      json.RawMessage `json:"mysql"`
		MySQLDB    json.RawMessage `json:"my_sql"`
		Redis      json.RawMessage `json:"redis"`
		TDengine   json.RawMessage `json:"tdengine"`
		TDengineDB json.RawMessage `json:"td_engine"`
	}

	var items []apiEnvItem
	if err := json.Unmarshal(body, &items); err != nil {
		var envelope struct {
			Data []apiEnvItem `json:"data"`
		}
		if err2 := json.Unmarshal(body, &envelope); err2 != nil {
			return nil, err
		}
		items = envelope.Data
	}

	out := make([]environmentRow, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Slug) == "" {
			continue
		}
		pgRaw := item.PostgreSQL
		if len(pgRaw) == 0 || string(pgRaw) == "null" {
			pgRaw = item.PostgresDB
		}
		myRaw := item.MySQL
		if len(myRaw) == 0 || string(myRaw) == "null" {
			myRaw = item.MySQLDB
		}
		tdRaw := item.TDengine
		if len(tdRaw) == 0 || string(tdRaw) == "null" {
			tdRaw = item.TDengineDB
		}
		out = append(out, environmentRow{
			Slug:     item.Slug,
			Name:     item.Name,
			ProxyRaw: item.Proxy,
			Postgres: pgRaw,
			MySQL:    myRaw,
			Redis:    item.Redis,
			TDengine: tdRaw,
		})
	}
	return out, nil
}

func queryProjectEnvVarsViaHTTP(ctx context.Context, baseURL string, token string, envs []environmentRow) map[string]map[string]string {
	result := make(map[string]map[string]string)
	client := opsHTTPClient()
	trimmedBase := strings.TrimRight(baseURL, "/")

	for _, env := range envs {
		listURL := fmt.Sprintf("%s/api/v1/projects/%s/env-files", trimmedBase, url.PathEscape(env.Slug))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var listEnv struct {
			Data struct {
				Configured bool `json:"configured"`
				Files      []struct {
					ID       string `json:"id"`
					Name     string `json:"name"`
					Revision int    `json:"revision"`
				} `json:"files"`
			} `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&listEnv)
		_ = resp.Body.Close()

		for _, f := range listEnv.Data.Files {
			if f.ID == "" || f.Revision <= 0 {
				continue
			}
			dlURL := fmt.Sprintf(
				"%s/api/v1/projects/%s/env-files/%s/download?revision=%d",
				trimmedBase,
				url.PathEscape(env.Slug),
				url.PathEscape(f.ID),
				f.Revision,
			)
			dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
			if err != nil {
				continue
			}
			dlReq.Header.Set("Authorization", "Bearer "+token)
			dlResp, err := client.Do(dlReq)
			if err != nil {
				continue
			}
			rawBytes, readErr := io.ReadAll(dlResp.Body)
			_ = dlResp.Body.Close()
			if readErr != nil || dlResp.StatusCode >= 400 {
				continue
			}
			parsed := ParseDotEnvText(string(rawBytes))
			if result[env.Slug] == nil {
				result[env.Slug] = make(map[string]string)
			}
			for k, v := range parsed {
				result[env.Slug][k] = v
			}
		}
	}
	return result
}

func resolveOpsControlPlaneDir() string {
	candidates := []string{
		strings.TrimSpace(os.Getenv("WHODB_OPS_CONTROL_PLANE_DIR")),
		defaultOpsControlPlaneDir,
		"../ops-control-plane",
		"../../ops-control-plane",
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			return abs
		}
	}
	return ""
}

func resolveEnvsDir(opsDir string, opsDotEnv map[string]string) string {
	if custom := strings.TrimSpace(os.Getenv("WHODB_OPS_ENVS_DIR")); custom != "" {
		if info, err := os.Stat(custom); err == nil && info.IsDir() {
			return custom
		}
	}
	if mwFile := strings.TrimSpace(opsDotEnv["MIDDLEWARE_ENV_FILE"]); mwFile != "" {
		if !filepath.IsAbs(mwFile) {
			mwFile = filepath.Clean(filepath.Join(opsDir, mwFile))
		}
		dir := filepath.Dir(mwFile)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	if info, err := os.Stat(defaultOpsEnvsDir); err == nil && info.IsDir() {
		return defaultOpsEnvsDir
	}
	return ""
}

func resolveOpsDatabaseDSN(opsDotEnv map[string]string, baselineVars map[string]string) string {
	for _, key := range []string{"WHODB_OPS_DATABASE_DSN", "DATABASE_DSN", "DATABASE_URL"} {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			return val
		}
		if val := strings.TrimSpace(opsDotEnv[key]); val != "" {
			return val
		}
	}

	host := firstNonEmpty(opsDotEnv["PG_DB_HOST"], baselineVars["PG_DB_HOST"])
	port := firstNonEmpty(opsDotEnv["PG_DB_PORT"], baselineVars["PG_DB_PORT"], "5432")
	user := firstNonEmpty(opsDotEnv["PG_DB_USER"], baselineVars["PG_DB_USER"], "postgres")
	password := firstNonEmpty(opsDotEnv["PG_DB_PASSWORD"], baselineVars["PG_DB_PASSWORD"])
	dbName := firstNonEmpty(opsDotEnv["OPS_DATABASE_NAME"], "ops_control_plane")

	if host == "" {
		return ""
	}

	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + dbName,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

func queryCustomerEnvironments(ctx context.Context, db *sql.DB) ([]environmentRow, error) {
	const q = `
		SELECT
			ce.slug,
			ce.name,
			COALESCE((
				SELECT eg.name
				FROM environment_group_members egm
				JOIN environment_groups eg ON eg.id = egm.group_id
				WHERE egm.env_slug = ce.slug
				ORDER BY eg.sort_order ASC, eg.name ASC
				LIMIT 1
			), '') AS group_name,
			ce.proxy,
			ce.postgre_sql,
			ce.my_sql,
			ce.redis,
			ce.td_engine
		FROM customer_environments ce
		ORDER BY ce.created_at ASC, ce.slug ASC
	`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make([]environmentRow, 0)
	for rows.Next() {
		var r environmentRow
		if err := rows.Scan(&r.Slug, &r.Name, &r.GroupName, &r.ProxyRaw, &r.Postgres, &r.MySQL, &r.Redis, &r.TDengine); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func queryProjectEnvVars(ctx context.Context, db *sql.DB, keyring map[string][]byte) map[string]map[string]string {
	result := make(map[string]map[string]string)
	if len(keyring) == 0 {
		return result
	}

	const q = `
		SELECT
			f.env_slug,
			f.id,
			v.revision,
			v.key_id,
			v.wrapped_key,
			v.encrypted_content
		FROM project_env_files f
		JOIN project_env_versions v
			ON v.file_id = f.id
			AND v.revision = f.revision
		WHERE f.deleted_at IS NULL
		ORDER BY f.env_slug ASC, f.name ASC
	`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return result
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			envSlug          string
			fileID           string
			revision         int
			keyID            string
			wrappedKey       []byte
			encryptedContent []byte
		)
		if err := rows.Scan(&envSlug, &fileID, &revision, &keyID, &wrappedKey, &encryptedContent); err != nil {
			continue
		}
		plaintext, err := DecryptProjectEnvVersion(keyring, envSlug, fileID, revision, keyID, wrappedKey, encryptedContent)
		if err != nil {
			continue
		}
		parsed := ParseDotEnvText(plaintext)
		if result[envSlug] == nil {
			result[envSlug] = make(map[string]string)
		}
		for k, v := range parsed {
			result[envSlug][k] = v
		}
	}

	return result
}

func parseProxyConfig(raw []byte) ProxyConfig {
	if len(raw) == 0 {
		return ProxyConfig{Type: "direct"}
	}
	var p ProxyConfig
	if err := json.Unmarshal(raw, &p); err != nil {
		return ProxyConfig{Type: "direct"}
	}
	p.Password = DecryptEnvironmentSecret(p.Password)
	p.SSHKey = DecryptEnvironmentSecret(p.SSHKey)
	return p
}

func detectPrimaryEnvironmentIP(proxy ProxyConfig, rawConfigs ...[]byte) string {
	for _, raw := range rawConfigs {
		var m dbMiddlewareJSON
		if err := json.Unmarshal(raw, &m); err == nil {
			host := strings.TrimSpace(m.Host)
			if host != "" && host != "127.0.0.1" && !strings.EqualFold(host, "localhost") {
				return host
			}
		}
	}
	if h := strings.TrimSpace(proxy.Host); h != "" && h != "127.0.0.1" && !strings.EqualFold(h, "localhost") {
		return h
	}
	return ""
}

func buildEnvironmentProfiles(
	env environmentRow,
	proxy ProxyConfig,
	envVars map[string]string,
	baselineVars map[string]string,
) []types.DatabaseCredentials {
	envLabel := strings.TrimSpace(env.Name)
	if envLabel == "" {
		envLabel = env.Slug
	}

	profiles := make([]types.DatabaseCredentials, 0, 6)

	addProfile := func(dbType string, host string, port string, user string, pass string, database string, config map[string]string) {
		host = strings.TrimSpace(host)
		port = strings.TrimSpace(port)
		if host == "" {
			return
		}
		RegisterTargetProxy(host, port, proxy)

		endpoint := host
		if port != "" {
			endpoint = net.JoinHostPort(host, port)
		}
		displayName := fmt.Sprintf("[%s] %s (%s)", envLabel, dbType, endpoint)
		profiles = append(profiles, types.DatabaseCredentials{
			ProjectID:   env.Slug,
			ProjectName: envLabel,
			Alias:       displayName,
			CustomId:    displayName,
			Type:        dbType,
			Hostname:    host,
			Port:        port,
			Username:    strings.TrimSpace(user),
			Password:    pass,
			Database:    strings.TrimSpace(database),
			Advanced:    config,
			Source:      "environment",
			IsProfile:   true,
		})
	}

	// 1. PostgreSQL
	pgCfg := parseDBMiddleware(env.Postgres)
	pgHost := firstNonEmpty(enabledVal(pgCfg.Enabled, pgCfg.Host), envVars["PG_DB_HOST"], envVars["POSTGRES_HOST"], pgCfg.Host)
	if pgCfg.Enabled || pgHost != "" {
		pgPort := firstNonEmpty(nonDefaultPort(pgCfg.Enabled, anyToString(pgCfg.Port), "5432"), envVars["PG_DB_PORT"], envVars["POSTGRES_PORT"], anyToString(pgCfg.Port), "5432")
		pgUser := firstNonEmpty(enabledVal(pgCfg.Enabled, pgCfg.Username), envVars["PG_DB_USER"], envVars["POSTGRES_USER"], pgCfg.Username, "postgres")
		pgPass := firstNonEmpty(enabledVal(pgCfg.Enabled, pgCfg.Password), envVars["PG_DB_PASSWORD"], envVars["POSTGRES_PASSWORD"], pgCfg.Password)
		if pgPass == "" && pgHost == strings.TrimSpace(baselineVars["PG_DB_HOST"]) {
			pgPass = baselineVars["PG_DB_PASSWORD"]
		}
		pgDB := firstNonEmpty(enabledVal(pgCfg.Enabled, anyToString(pgCfg.Database)), envVars["PG_DB_NAME"], envVars["POSTGRES_DB"], anyToString(pgCfg.Database), "postgres")
		addProfile(engine.DatabaseType_Postgres, pgHost, pgPort, pgUser, pgPass, pgDB, nil)
	}

	// 2. MySQL
	myCfg := parseDBMiddleware(env.MySQL)
	myHost := firstNonEmpty(enabledVal(myCfg.Enabled, myCfg.Host), envVars["DB_HOST"], envVars["MYSQL_HOST"], myCfg.Host)
	if myCfg.Enabled || myHost != "" {
		myPort := firstNonEmpty(nonDefaultPort(myCfg.Enabled, anyToString(myCfg.Port), "3306"), envVars["DB_PORT"], envVars["MYSQL_PORT"], anyToString(myCfg.Port), "3306")
		myUser := firstNonEmpty(enabledVal(myCfg.Enabled, myCfg.Username), envVars["DB_USER"], envVars["MYSQL_USER"], myCfg.Username, "root")
		myPass := firstNonEmpty(enabledVal(myCfg.Enabled, myCfg.Password), envVars["DB_PASSWORD"], envVars["MYSQL_PASSWORD"], myCfg.Password)
		if myPass == "" && myHost == strings.TrimSpace(baselineVars["DB_HOST"]) {
			myPass = baselineVars["DB_PASSWORD"]
		}
		myDB := firstNonEmpty(enabledVal(myCfg.Enabled, anyToString(myCfg.Database)), envVars["DB_NAME"], envVars["MYSQL_DATABASE"], anyToString(myCfg.Database))
		addProfile(engine.DatabaseType_MySQL, myHost, myPort, myUser, myPass, myDB, nil)
	}

	// 3. Redis
	redisCfg := parseDBMiddleware(env.Redis)
	redisHost := firstNonEmpty(enabledVal(redisCfg.Enabled, redisCfg.Host), envVars["REDIS_HOST"], redisCfg.Host)
	if redisCfg.Enabled || redisHost != "" {
		redisPort := firstNonEmpty(nonDefaultPort(redisCfg.Enabled, anyToString(redisCfg.Port), "6379"), envVars["REDIS_PORT"], anyToString(redisCfg.Port), "6379")
		redisUser := firstNonEmpty(enabledVal(redisCfg.Enabled, redisCfg.Username), envVars["REDIS_USERNAME"], redisCfg.Username)
		redisPass := firstNonEmpty(enabledVal(redisCfg.Enabled, redisCfg.Password), envVars["REDIS_PASSWORD"], redisCfg.Password)
		if redisPass == "" && redisHost == strings.TrimSpace(baselineVars["REDIS_HOST"]) {
			redisPass = baselineVars["REDIS_PASSWORD"]
		}
		redisDB := firstNonEmpty(enabledVal(redisCfg.Enabled, anyToString(redisCfg.Database)), envVars["REDIS_DB"], anyToString(redisCfg.Database), "0")
		addProfile(engine.DatabaseType_Redis, redisHost, redisPort, redisUser, redisPass, redisDB, nil)
	}

	// 4. TDengine
	tdCfg := parseDBMiddleware(env.TDengine)
	tdURLHost, tdURLPort, tdURLDB := parseJDBCOrHTTPEndpoint(envVars["TDENGINE_URL"])
	tdHost := firstNonEmpty(enabledVal(tdCfg.Enabled, tdCfg.Host), tdURLHost, envVars["TAOS_HOST"], envVars["TDENGINE_HOST"], tdCfg.Host)
	if tdCfg.Enabled || tdHost != "" {
		tdPort := firstNonEmpty(nonDefaultPort(tdCfg.Enabled, anyToString(tdCfg.Port), "6041"), tdURLPort, envVars["TAOS_PORT"], envVars["TDENGINE_PORT"], anyToString(tdCfg.Port), "6041")
		tdUser := firstNonEmpty(enabledVal(tdCfg.Enabled, tdCfg.Username), envVars["TDENGINE_USERNAME"], envVars["TAOS_USERNAME"], tdCfg.Username, "root")
		tdPass := firstNonEmpty(enabledVal(tdCfg.Enabled, tdCfg.Password), envVars["TDENGINE_PASSWORD"], envVars["TAOS_PASSWORD"], tdCfg.Password)
		if tdPass == "" {
			baseTDHost, _, _ := parseJDBCOrHTTPEndpoint(baselineVars["TDENGINE_URL"])
			if tdHost == baseTDHost || tdHost == strings.TrimSpace(baselineVars["SERVER_IP_DB"]) {
				tdPass = baselineVars["TDENGINE_PASSWORD"]
			}
		}
		tdDB := firstNonEmpty(enabledVal(tdCfg.Enabled, anyToString(tdCfg.Database)), tdURLDB, envVars["TDENGINE_DATABASE"], envVars["TAOS_DATABASE"], anyToString(tdCfg.Database), "thingsboard")
		addProfile(engine.DatabaseType_TDengine, tdHost, tdPort, tdUser, tdPass, tdDB, nil)
	}

	// 5. ClickHouse (from project/environment .env)
	chURLHost, chURLPort, chURLDB := parseJDBCOrHTTPEndpoint(envVars["CLICKHOUSE_JDBC_URL"])
	chHost := firstNonEmpty(chURLHost, envVars["CLICKHOUSE_HOST"])
	if chHost != "" {
		chPort := firstNonEmpty(chURLPort, envVars["CLICKHOUSE_PORT"], "8123")
		chUser := firstNonEmpty(envVars["CLICKHOUSE_USER"], envVars["CLICKHOUSE_USERNAME"], "default")
		chPass := envVars["CLICKHOUSE_PASSWORD"]
		chDB := firstNonEmpty(chURLDB, envVars["CLICKHOUSE_DB"], envVars["CLICKHOUSE_DATABASE"], "default")
		var extra map[string]string
		if chURLHost != "" || chPort == "8123" || chPort == "30123" || chPort == "8443" {
			extra = map[string]string{"HTTP Protocol": "enable"}
		}
		addProfile(engine.DatabaseType_ClickHouse, chHost, chPort, chUser, chPass, chDB, extra)
	}

	// 6. ElasticSearch (from project/environment .env)
	esURLHost, esURLPort, _ := parseJDBCOrHTTPEndpoint(envVars["ELASTICSEARCH_HTTP_URL"])
	esHost := firstNonEmpty(esURLHost, envVars["ELASTICSEARCH_HOST"])
	if esHost != "" {
		esPort := firstNonEmpty(esURLPort, envVars["ELASTICSEARCH_PORT"], "9200")
		esPass := envVars["ELASTICSEARCH_PASSWORD"]
		esUser := firstNonEmpty(envVars["ELASTICSEARCH_USERNAME"], envVars["ELASTICSEARCH_USER"])
		if esUser == "" && esPass != "" {
			esUser = "elastic"
		}
		addProfile(engine.DatabaseType_ElasticSearch, esHost, esPort, esUser, esPass, "", nil)
	}

	return profiles
}

func parseDBMiddleware(raw []byte) dbMiddlewareJSON {
	var m dbMiddlewareJSON
	if len(raw) == 0 {
		return m
	}
	_ = json.Unmarshal(raw, &m)
	m.Password = DecryptEnvironmentSecret(m.Password)
	return m
}

func parseJDBCOrHTTPEndpoint(raw string) (host string, port string, database string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", ""
	}
	for _, prefix := range []string{
		"jdbc:TAOS-WS://", "jdbc:TAOS-RS://", "jdbc:TAOS://",
		"jdbc:taos-ws://", "jdbc:taos-rs://", "jdbc:taos://",
		"jdbc:clickhouse://", "jdbc:ch://",
		"jdbc:mysql://", "jdbc:postgresql://",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			trimmed = "http://" + strings.TrimPrefix(trimmed, prefix)
			break
		}
	}
	if !strings.Contains(trimmed, "://") {
		if h, p, err := net.SplitHostPort(trimmed); err == nil {
			return h, p, ""
		}
		return trimmed, "", ""
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", "", ""
	}
	return u.Hostname(), u.Port(), strings.Trim(u.Path, "/")
}

func loadLocalEnvFiles(envsDir string) (bySlug map[string]map[string]string, byIP map[string]map[string]string) {
	bySlug = make(map[string]map[string]string)
	byIP = make(map[string]map[string]string)
	if envsDir == "" {
		return bySlug, byIP
	}

	entries, err := os.ReadDir(envsDir)
	if err != nil {
		return bySlug, byIP
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".env") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".env")
		vars := readDotEnvFile(filepath.Join(envsDir, entry.Name()))
		if len(vars) == 0 {
			continue
		}
		bySlug[stem] = vars
		for _, ipKey := range []string{"SERVER_IP_DB", "DB_HOST", "PG_DB_HOST"} {
			if ip := strings.TrimSpace(vars[ipKey]); ip != "" && ip != "127.0.0.1" && !strings.EqualFold(ip, "localhost") {
				byIP[ip] = vars
			}
		}
	}
	return bySlug, byIP
}

func readDotEnvFile(path string) map[string]string {
	if strings.TrimSpace(path) == "" {
		return map[string]string{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	return ParseDotEnvText(string(data))
}

// ParseDotEnvText parses a standard .env file body into a key-value map.
func ParseDotEnvText(content string) map[string]string {
	values := make(map[string]string)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		values[key] = val
	}
	return values
}

func anyToString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case float64:
		if val <= 0 {
			return ""
		}
		return strconv.FormatInt(int64(val), 10)
	case int:
		if val <= 0 {
			return ""
		}
		return strconv.Itoa(val)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", val))
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func enabledVal(enabled bool, val string) string {
	if !enabled {
		return ""
	}
	return strings.TrimSpace(val)
}

func nonDefaultPort(enabled bool, port string, defaultPort string) string {
	p := strings.TrimSpace(port)
	if !enabled || p == "" || p == defaultPort {
		return ""
	}
	return p
}
