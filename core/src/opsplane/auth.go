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
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/clidey/whodb/core/src/log"
)

const defaultOpsPlatformURL = "https://ops.mozyun.cn"

// AuthStatus represents the current authentication and profile sync state with ops-control-plane.
type AuthStatus struct {
	Authenticated    bool     `json:"authenticated"`
	Username         string   `json:"username"`
	Role             string   `json:"role"`
	Endpoint         string   `json:"endpoint"`
	Token            string   `json:"token,omitempty"`
	AllowAllEnvs     bool     `json:"allowAllEnvs"`
	AllowedEnvSlugs  []string `json:"allowedEnvSlugs"`
	EnvironmentCount int      `json:"environmentCount"`
	ProfileCount     int      `json:"profileCount"`
	SyncedAt         string   `json:"syncedAt"`
}

var (
	authMu        sync.Mutex
	authLoaded    bool
	activeSession *AuthStatus
)

// GetAuthStatus returns the current ops-control-plane login and sync status.
func GetAuthStatus() AuthStatus {
	session := loadOrGetSession()
	if session == nil || !session.Authenticated {
		return AuthStatus{
			Authenticated: false,
			Endpoint:      defaultDisplayEndpoint(),
		}
	}

	profiles, envsCount, err := getOrDiscoverProfilesForSession(session)
	if err == nil {
		authMu.Lock()
		if activeSession != nil && activeSession.Authenticated {
			activeSession.ProfileCount = len(profiles)
			activeSession.EnvironmentCount = envsCount
			if activeSession.SyncedAt == "" {
				activeSession.SyncedAt = time.Now().Format(time.RFC3339)
			}
			cp := *activeSession
			authMu.Unlock()
			return cp
		}
		authMu.Unlock()
	}

	return *session
}

// Login authenticates a user against ops-control-plane and immediately syncs all authorized database profiles.
func Login(ctx context.Context, endpoint string, username string, password string) (*AuthStatus, error) {
	username = strings.TrimSpace(username)
	endpoint = strings.TrimSpace(endpoint)
	if username == "" || password == "" {
		return nil, errors.New("请输入 Ops Control Plane 用户名和密码")
	}

	status, err := authenticateUser(ctx, endpoint, username, password)
	if err != nil {
		return nil, err
	}

	profiles, envCount, err := discoverProfilesForSession(status)
	if err != nil {
		return nil, fmt.Errorf("同步 Ops Control Plane 数据库配置失败: %w", err)
	}

	status.Authenticated = true
	status.EnvironmentCount = envCount
	status.ProfileCount = len(profiles)
	status.SyncedAt = time.Now().Format(time.RFC3339)
	if status.Endpoint == "" {
		status.Endpoint = defaultDisplayEndpoint()
	}

	authMu.Lock()
	authLoaded = true
	cp := *status
	activeSession = &cp
	saveSessionToDisk(&cp)
	authMu.Unlock()

	cacheMu.Lock()
	cachedProfiles = slices.Clone(profiles)
	cachedEnvCount = envCount
	cacheExpiresAt = time.Now().Add(profileCacheTTL)
	cacheMu.Unlock()

	return status, nil
}

// SyncProfiles refreshes the user's RBAC permissions and re-discovers all authorized database profiles.
func SyncProfiles(ctx context.Context) (*AuthStatus, error) {
	session := loadOrGetSession()
	if session == nil || !session.Authenticated {
		return nil, errors.New("请先登录 Ops Control Plane 平台")
	}

	if updated, err := refreshUserPermissionsFromDB(ctx, session.Endpoint, session.Username); err == nil && updated != nil {
		session.Role = updated.Role
		session.AllowAllEnvs = updated.AllowAllEnvs
		session.AllowedEnvSlugs = updated.AllowedEnvSlugs
	}

	InvalidateProfileCache()
	profiles, envCount, err := discoverProfilesForSession(session)
	if err != nil {
		return nil, err
	}

	session.EnvironmentCount = envCount
	session.ProfileCount = len(profiles)
	session.SyncedAt = time.Now().Format(time.RFC3339)

	authMu.Lock()
	cp := *session
	activeSession = &cp
	saveSessionToDisk(&cp)
	authMu.Unlock()

	cacheMu.Lock()
	cachedProfiles = slices.Clone(profiles)
	cachedEnvCount = envCount
	cacheExpiresAt = time.Now().Add(profileCacheTTL)
	cacheMu.Unlock()

	return &cp, nil
}

// Logout clears the authenticated ops-control-plane session and removes all synced profiles from memory.
func Logout() {
	authMu.Lock()
	authLoaded = true
	activeSession = nil
	if path := sessionFilePath(); path != "" {
		_ = os.Remove(path)
	}
	authMu.Unlock()

	InvalidateProfileCache()
}

// InvalidateProfileCache clears the in-memory profile discovery cache.
func InvalidateProfileCache() {
	cacheMu.Lock()
	cachedProfiles = nil
	cachedEnvCount = 0
	cacheExpiresAt = time.Time{}
	cacheMu.Unlock()
}

// SetSessionForTesting sets an in-memory session for unit and integration tests without writing to disk.
func SetSessionForTesting(session *AuthStatus) {
	authMu.Lock()
	authLoaded = true
	if session == nil {
		activeSession = nil
	} else {
		cp := *session
		activeSession = &cp
	}
	authMu.Unlock()
	InvalidateProfileCache()
}

func loadOrGetSession() *AuthStatus {
	authMu.Lock()
	defer authMu.Unlock()

	if !authLoaded {
		authLoaded = true
		if path := sessionFilePath(); path != "" {
			if data, err := os.ReadFile(path); err == nil {
				var s AuthStatus
				if err := json.Unmarshal(data, &s); err == nil && s.Authenticated && s.Username != "" {
					s.Endpoint = normalizePlatformEndpoint(s.Endpoint)
					activeSession = &s
				}
			}
		}
	}

	if activeSession == nil {
		return nil
	}
	cp := *activeSession
	cp.AllowedEnvSlugs = slices.Clone(activeSession.AllowedEnvSlugs)
	return &cp
}

func saveSessionToDisk(session *AuthStatus) {
	path := sessionFilePath()
	if path == "" || session == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.WithError(err).Warn("Failed to create ops-control-plane session directory")
		return
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

func sessionFilePath() string {
	if custom := strings.TrimSpace(os.Getenv("WHODB_OPS_SESSION_FILE")); custom != "" {
		return custom
	}
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		return filepath.Join(home, ".whodb", "ops_control_plane_session.json")
	}
	return filepath.Join(configDir, "whodb", "ops_control_plane_session.json")
}

func defaultDisplayEndpoint() string {
	for _, key := range []string{"WHODB_OPS_PLATFORM_URL", "OPS_CONTROL_PLANE_URL"} {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			if normalized := normalizePlatformURL(val); normalized != "" {
				return normalized
			}
		}
	}

	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		opsBrowserDir := filepath.Join(configDir, "cn.mozyun.ops-browser")
		for _, file := range []string{"login-profile.json", "session.json"} {
			if data, err := os.ReadFile(filepath.Join(opsBrowserDir, file)); err == nil {
				var parsed struct {
					APIBase string `json:"api_base"`
				}
				if json.Unmarshal(data, &parsed) == nil {
					if normalized := normalizePlatformURL(parsed.APIBase); normalized != "" {
						return normalized
					}
				}
			}
		}
	}

	return defaultOpsPlatformURL
}

func normalizePlatformEndpoint(raw string) string {
	if normalized := normalizePlatformURL(raw); normalized != "" {
		return normalized
	}
	return defaultDisplayEndpoint()
}

func normalizePlatformURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" ||
		strings.HasPrefix(trimmed, "postgres://") ||
		strings.HasPrefix(trimmed, "postgresql://") ||
		strings.HasSuffix(trimmed, "/ops_control_plane") {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "127.0.0.1") ||
			strings.HasPrefix(lower, "localhost") ||
			strings.HasPrefix(lower, "10.") ||
			strings.HasPrefix(lower, "192.168.") ||
			strings.HasPrefix(lower, "100.64.") {
			trimmed = "http://" + trimmed
		} else {
			trimmed = "https://" + trimmed
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/api/v1")
	return strings.TrimSuffix(u.String(), "/")
}

var sharedOpsHTTPClient = &http.Client{
	Timeout: 12 * time.Second,
	Transport: &http.Transport{
		Proxy:                nil,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     60 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // ops.mozyun.cn uses an internal CA certificate
		},
	},
}

func opsHTTPClient() *http.Client {
	return sharedOpsHTTPClient
}

type authRejectedError struct {
	msg string
}

func (e *authRejectedError) Error() string {
	return e.msg
}

func authenticateUser(ctx context.Context, endpoint string, username string, password string) (*AuthStatus, error) {
	platformURL := normalizePlatformEndpoint(endpoint)
	status, httpErr := authenticateViaHTTP(ctx, platformURL, username, password)
	if httpErr == nil {
		return status, nil
	}

	dbStatus, dbErr := authenticateViaDatabase(ctx, endpoint, username, password)
	if dbErr == nil {
		dbStatus.Endpoint = platformURL
		return dbStatus, nil
	}

	var rejected *authRejectedError
	if errors.As(httpErr, &rejected) {
		return nil, rejected
	}
	return nil, dbErr
}

type httpLoginResponse struct {
	Code    int    `json:"code"`
	Success *bool  `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Data    struct {
		Token    string `json:"token"`
		ExpireAt string `json:"expire_at"`
		User     struct {
			Username               string   `json:"username"`
			Role                   string   `json:"role"`
			IsActive               bool     `json:"is_active"`
			EnvironmentPermissions []string `json:"environment_permissions"`
			Roles                  []struct {
				Code             string   `json:"code"`
				Name             string   `json:"name"`
				ScopeType        string   `json:"scope_type"`
				EnvironmentSlugs []string `json:"environment_slugs"`
			} `json:"roles"`
		} `json:"user"`
	} `json:"data"`
}

func authenticateViaHTTP(ctx context.Context, baseURL string, username string, password string) (*AuthStatus, error) {
	loginURL := strings.TrimRight(baseURL, "/") + "/api/v1/auth/login"
	bodyBytes, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		return nil, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, loginURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	resp, err := opsHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var parsed httpLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	explicitFailure := parsed.Code != 0 || (parsed.Success != nil && !*parsed.Success)
	hasAuthPayload := parsed.Data.Token != "" || parsed.Data.User.Username != ""
	if resp.StatusCode >= 400 || explicitFailure || !hasAuthPayload {
		msg := firstNonEmpty(parsed.Message, parsed.Error, "用户名或密码错误")
		return nil, &authRejectedError{msg: msg}
	}

	allowAll := strings.EqualFold(parsed.Data.User.Role, "admin") || slices.Contains(parsed.Data.User.EnvironmentPermissions, "*")
	allowed := make([]string, 0)
	for _, slug := range parsed.Data.User.EnvironmentPermissions {
		if slug != "" && slug != "*" && !slices.Contains(allowed, slug) {
			allowed = append(allowed, slug)
		}
	}
	var roleNames []string
	for _, r := range parsed.Data.User.Roles {
		if r.Name != "" && !slices.Contains(roleNames, r.Name) {
			roleNames = append(roleNames, r.Name)
		}
		if strings.EqualFold(r.ScopeType, "global") || strings.EqualFold(r.Code, "super_admin") {
			allowAll = true
		}
		for _, slug := range r.EnvironmentSlugs {
			if slug == "*" {
				allowAll = true
			} else if slug != "" && !slices.Contains(allowed, slug) {
				allowed = append(allowed, slug)
			}
		}
	}
	roleLabel := parsed.Data.User.Role
	if len(roleNames) > 0 {
		roleLabel = strings.Join(roleNames, ", ")
	}

	return &AuthStatus{
		Authenticated:   true,
		Username:        firstNonEmpty(parsed.Data.User.Username, username),
		Role:            firstNonEmpty(roleLabel, "user"),
		Endpoint:        baseURL,
		Token:           parsed.Data.Token,
		AllowAllEnvs:    allowAll,
		AllowedEnvSlugs: allowed,
	}, nil
}

func authenticateViaDatabase(ctx context.Context, customEndpoint string, username string, password string) (*AuthStatus, error) {
	dsn, displayEndpoint := resolveAuthDSN(customEndpoint)
	if dsn == "" {
		return nil, errors.New("未找到 Ops Control Plane 数据库连接配置")
	}

	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	var (
		userID       int64
		dbUsername   string
		passwordHash string
		role         sql.NullString
		isActive     sql.NullBool
		envPermsRaw  sql.NullString
	)
	const userQuery = `
		SELECT id, username, password, COALESCE(role, 'user'), COALESCE(is_active, true), COALESCE(environment_permissions, '[]')
		FROM users
		WHERE username = $1
		LIMIT 1
	`
	err = db.QueryRowContext(queryCtx, userQuery, username).Scan(
		&userID,
		&dbUsername,
		&passwordHash,
		&role,
		&isActive,
		&envPermsRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("用户名或密码错误")
	}
	if err != nil {
		return nil, fmt.Errorf("连接 Ops Control Plane 认证库失败: %w", err)
	}

	if isActive.Valid && !isActive.Bool {
		return nil, errors.New("该账号已被禁用")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, errors.New("用户名或密码错误")
	}

	allowAll, allowedSlugs, roleDisplay := queryUserEnvironmentAccess(queryCtx, db, userID, role.String, envPermsRaw.String)
	return &AuthStatus{
		Authenticated:   true,
		Username:        dbUsername,
		Role:            roleDisplay,
		Endpoint:        displayEndpoint,
		AllowAllEnvs:    allowAll,
		AllowedEnvSlugs: allowedSlugs,
	}, nil
}

func refreshUserPermissionsFromDB(ctx context.Context, customEndpoint string, username string) (*AuthStatus, error) {
	dsn, displayEndpoint := resolveAuthDSN(customEndpoint)
	if dsn == "" {
		return nil, errors.New("missing DSN")
	}

	queryCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	var (
		userID      int64
		role        sql.NullString
		isActive    sql.NullBool
		envPermsRaw sql.NullString
	)
	const userQuery = `
		SELECT id, COALESCE(role, 'user'), COALESCE(is_active, true), COALESCE(environment_permissions, '[]')
		FROM users
		WHERE username = $1
		LIMIT 1
	`
	if err := db.QueryRowContext(queryCtx, userQuery, username).Scan(&userID, &role, &isActive, &envPermsRaw); err != nil {
		return nil, err
	}
	if isActive.Valid && !isActive.Bool {
		return nil, errors.New("user disabled")
	}

	allowAll, allowedSlugs, roleDisplay := queryUserEnvironmentAccess(queryCtx, db, userID, role.String, envPermsRaw.String)
	return &AuthStatus{
		Authenticated:   true,
		Username:        username,
		Role:            roleDisplay,
		Endpoint:        displayEndpoint,
		AllowAllEnvs:    allowAll,
		AllowedEnvSlugs: allowedSlugs,
	}, nil
}

func queryUserEnvironmentAccess(
	ctx context.Context,
	db *sql.DB,
	userID int64,
	legacyRole string,
	legacyEnvPermsRaw string,
) (allowAll bool, allowedSlugs []string, roleDisplay string) {
	roleDisplay = strings.TrimSpace(legacyRole)
	if strings.EqualFold(roleDisplay, "admin") {
		allowAll = true
	}

	legacySlugs := parseStringArrayColumn(legacyEnvPermsRaw)
	for _, s := range legacySlugs {
		if s == "*" {
			allowAll = true
		} else if s != "" && !slices.Contains(allowedSlugs, s) {
			allowedSlugs = append(allowedSlugs, s)
		}
	}

	const assignmentQuery = `
		SELECT
			a.id,
			a.scope_type,
			r.id,
			r.name,
			r.is_super_admin
		FROM rbac_user_role_assignments a
		JOIN rbac_roles r ON r.id = a.role_id
		WHERE a.user_id = $1
		  AND a.is_active = true
		  AND r.is_active = true
		  AND (a.expires_at IS NULL OR a.expires_at > NOW())
		ORDER BY r.is_super_admin DESC, r.id ASC
	`
	rows, err := db.QueryContext(ctx, assignmentQuery, userID)
	if err != nil {
		return allowAll, allowedSlugs, firstNonEmpty(roleDisplay, "user")
	}
	defer func() { _ = rows.Close() }()

	type grantRow struct {
		assignmentID int64
		scopeType    string
		roleID       int64
		roleName     string
		isSuperAdmin bool
	}
	var grants []grantRow
	var roleNames []string
	for rows.Next() {
		var g grantRow
		if err := rows.Scan(&g.assignmentID, &g.scopeType, &g.roleID, &g.roleName, &g.isSuperAdmin); err != nil {
			continue
		}
		grants = append(grants, g)
		if g.roleName != "" && !slices.Contains(roleNames, g.roleName) {
			roleNames = append(roleNames, g.roleName)
		}
		if g.isSuperAdmin || strings.EqualFold(g.scopeType, "global") {
			allowAll = true
		}
	}

	if len(roleNames) > 0 {
		roleDisplay = strings.Join(roleNames, ", ")
	}
	if allowAll || len(grants) == 0 {
		return allowAll, allowedSlugs, firstNonEmpty(roleDisplay, "user")
	}

	for _, g := range grants {
		// Direct assignment environments
		envRows, err := db.QueryContext(ctx, `SELECT env_slug FROM rbac_assignment_environments WHERE assignment_id = $1`, g.assignmentID)
		if err == nil {
			for envRows.Next() {
				var slug string
				if err := envRows.Scan(&slug); err == nil && slug != "" && !slices.Contains(allowedSlugs, slug) {
					allowedSlugs = append(allowedSlugs, slug)
				}
			}
			_ = envRows.Close()
		}

		// Role environment group members
		grpRows, err := db.QueryContext(ctx, `
			SELECT egm.env_slug
			FROM rbac_role_environment_groups reg
			JOIN environment_group_members egm ON egm.group_id = reg.group_id
			WHERE reg.role_id = $1
		`, g.roleID)
		if err == nil {
			for grpRows.Next() {
				var slug string
				if err := grpRows.Scan(&slug); err == nil && slug != "" && !slices.Contains(allowedSlugs, slug) {
					allowedSlugs = append(allowedSlugs, slug)
				}
			}
			_ = grpRows.Close()
		}
	}

	return allowAll, allowedSlugs, firstNonEmpty(roleDisplay, "user")
}

func parseStringArrayColumn(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
		return arr
	}
	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func resolveAuthDSN(customEndpoint string) (dsn string, displayEndpoint string) {
	displayEndpoint = normalizePlatformEndpoint(customEndpoint)
	opsDir := resolveOpsControlPlaneDir()
	var opsDotEnv, baselineVars map[string]string
	if opsDir != "" {
		opsDotEnv = readDotEnvFile(filepath.Join(opsDir, ".env"))
		envsDir := resolveEnvsDir(opsDir, opsDotEnv)
		baselineEnvPath := strings.TrimSpace(opsDotEnv["MIDDLEWARE_ENV_FILE"])
		if baselineEnvPath != "" && !filepath.IsAbs(baselineEnvPath) {
			baselineEnvPath = filepath.Clean(filepath.Join(opsDir, baselineEnvPath))
		}
		if baselineEnvPath == "" && envsDir != "" {
			baselineEnvPath = filepath.Join(envsDir, "100.env")
		}
		baselineVars = readDotEnvFile(baselineEnvPath)
	}

	baseDSN := resolveOpsDatabaseDSN(opsDotEnv, baselineVars)
	trimmedCustom := strings.TrimSpace(customEndpoint)
	if strings.HasPrefix(trimmedCustom, "postgres://") || strings.HasPrefix(trimmedCustom, "postgresql://") {
		return trimmedCustom, displayEndpoint
	}

	return baseDSN, displayEndpoint
}
