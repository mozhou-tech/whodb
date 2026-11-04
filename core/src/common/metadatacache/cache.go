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

// Package metadatacache provides a two-tier (in-memory L1 + SQLite3 L2)
// persistent metadata cache for database schemas, objects, and columns.
package metadatacache

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3" // Register standard sqlite3 driver for metadata cache

	"github.com/clidey/whodb/core/src/common/datadir"
	"github.com/clidey/whodb/core/src/env"
	"github.com/clidey/whodb/core/src/log"
)

const cacheDBFileName = "metadata_cache.db"

type memoryKey struct {
	scope string
	key   string
}

type memoryEntry struct {
	payload       []byte
	softExpiresAt int64
	hardExpiresAt int64
}

// Store manages an L1 in-memory cache backed by an L2 SQLite3 database file.
type Store struct {
	mu         sync.RWMutex
	mem        map[memoryKey]memoryEntry
	db         *sql.DB
	refreshing sync.Map
}

var (
	defaultStore     *Store
	defaultStoreOnce sync.Once
)

// Default returns the process-wide metadata cache store backed by SQLite3 in
// the WhoDB application data directory.
func Default() *Store {
	defaultStoreOnce.Do(func() {
		dir, err := datadir.Get(datadir.Options{
			AppName:           "whodb",
			EnterpriseEdition: env.IsEnterpriseEdition,
			Development:       env.IsDevelopment,
		})
		if err != nil {
			log.WithError(err).Warn("Failed to resolve data directory for metadata cache; using memory-only cache")
			defaultStore = &Store{mem: make(map[memoryKey]memoryEntry)}
			return
		}
		store, err := Open(filepath.Join(dir, cacheDBFileName))
		if err != nil {
			log.WithError(err).Warn("Failed to open SQLite3 metadata cache; using memory-only cache")
			defaultStore = &Store{mem: make(map[memoryKey]memoryEntry)}
			return
		}
		defaultStore = store
	})
	return defaultStore
}

// Open creates or opens a SQLite3-backed metadata cache store at dbPath.
func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, err
	}

	dsn := dbPath + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)

	const schemaSQL = `
		CREATE TABLE IF NOT EXISTS metadata_cache (
			scope TEXT NOT NULL,
			cache_key TEXT NOT NULL,
			payload BLOB NOT NULL,
			soft_expires_at INTEGER NOT NULL,
			hard_expires_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (scope, cache_key)
		);
		CREATE INDEX IF NOT EXISTS idx_metadata_cache_hard_expires ON metadata_cache(hard_expires_at);
	`
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Best-effort cleanup of hard-expired rows on startup.
	_, _ = db.Exec(`DELETE FROM metadata_cache WHERE hard_expires_at <= ?`, time.Now().UnixMilli())

	return &Store{
		mem: make(map[memoryKey]memoryEntry),
		db:  db,
	}, nil
}

// Get retrieves a cached value for (scope, key) and JSON-unmarshals it into dest.
// Returns hit=true when a valid or soft-expired entry was found, and stale=true
// when the entry has passed its soft TTL and should be refreshed in the background.
func (s *Store) Get(scope string, key string, dest any) (hit bool, stale bool) {
	if s == nil || scope == "" || key == "" {
		return false, false
	}

	now := time.Now().UnixMilli()
	mk := memoryKey{scope: scope, key: key}

	s.mu.RLock()
	entry, ok := s.mem[mk]
	s.mu.RUnlock()

	if ok {
		if now >= entry.hardExpiresAt {
			s.Delete(scope, key)
			return false, false
		}
		if err := json.Unmarshal(entry.payload, dest); err == nil {
			return true, now >= entry.softExpiresAt
		}
	}

	if s.db == nil {
		return false, false
	}

	var payload []byte
	var softExp, hardExp int64
	err := s.db.QueryRow(
		`SELECT payload, soft_expires_at, hard_expires_at FROM metadata_cache WHERE scope = ? AND cache_key = ?`,
		scope,
		key,
	).Scan(&payload, &softExp, &hardExp)
	if err != nil {
		return false, false
	}

	if now >= hardExp {
		s.Delete(scope, key)
		return false, false
	}

	if err := json.Unmarshal(payload, dest); err != nil {
		s.Delete(scope, key)
		return false, false
	}

	s.mu.Lock()
	s.mem[mk] = memoryEntry{
		payload:       payload,
		softExpiresAt: softExp,
		hardExpiresAt: hardExp,
	}
	s.mu.Unlock()

	return true, now >= softExp
}

// Set serializes value as JSON and stores it in both L1 memory and L2 SQLite3.
func (s *Store) Set(scope string, key string, value any, softTTL time.Duration, hardTTL time.Duration) {
	if s == nil || scope == "" || key == "" {
		return
	}
	if hardTTL < softTTL {
		hardTTL = softTTL
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return
	}

	now := time.Now()
	softExp := now.Add(softTTL).UnixMilli()
	hardExp := now.Add(hardTTL).UnixMilli()
	updatedAt := now.UnixMilli()

	mk := memoryKey{scope: scope, key: key}
	s.mu.Lock()
	s.mem[mk] = memoryEntry{
		payload:       payload,
		softExpiresAt: softExp,
		hardExpiresAt: hardExp,
	}
	s.mu.Unlock()

	if s.db == nil {
		return
	}

	_, _ = s.db.Exec(
		`INSERT INTO metadata_cache (scope, cache_key, payload, soft_expires_at, hard_expires_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(scope, cache_key) DO UPDATE SET
			payload = excluded.payload,
			soft_expires_at = excluded.soft_expires_at,
			hard_expires_at = excluded.hard_expires_at,
			updated_at = excluded.updated_at`,
		scope,
		key,
		payload,
		softExp,
		hardExp,
		updatedAt,
	)
}

// Delete removes a single key from L1 memory and L2 SQLite3.
func (s *Store) Delete(scope string, key string) {
	if s == nil || scope == "" || key == "" {
		return
	}

	s.mu.Lock()
	delete(s.mem, memoryKey{scope: scope, key: key})
	s.mu.Unlock()

	if s.db != nil {
		_, _ = s.db.Exec(`DELETE FROM metadata_cache WHERE scope = ? AND cache_key = ?`, scope, key)
	}
}

// InvalidateScope removes all cached entries belonging to the given scope.
func (s *Store) InvalidateScope(scope string) {
	if s == nil || scope == "" {
		return
	}

	s.mu.Lock()
	for mk := range s.mem {
		if mk.scope == scope {
			delete(s.mem, mk)
		}
	}
	s.mu.Unlock()

	if s.db != nil {
		_, _ = s.db.Exec(`DELETE FROM metadata_cache WHERE scope = ?`, scope)
	}
}

// InvalidatePrefix removes all cached entries in scope whose key starts with prefix.
func (s *Store) InvalidatePrefix(scope string, prefix string) {
	if s == nil || scope == "" {
		return
	}

	s.mu.Lock()
	for mk := range s.mem {
		if mk.scope == scope && strings.HasPrefix(mk.key, prefix) {
			delete(s.mem, mk)
		}
	}
	s.mu.Unlock()

	if s.db != nil {
		_, _ = s.db.Exec(`DELETE FROM metadata_cache WHERE scope = ? AND cache_key LIKE ?`, scope, prefix+"%")
	}
}

// TriggerRefresh runs fn asynchronously in the background if a refresh for
// (scope, key) is not already in flight.
func (s *Store) TriggerRefresh(scope string, key string, fn func()) {
	if s == nil || fn == nil {
		return
	}
	mk := memoryKey{scope: scope, key: key}
	if _, loaded := s.refreshing.LoadOrStore(mk, struct{}{}); loaded {
		return
	}
	go func() {
		defer s.refreshing.Delete(mk)
		fn()
	}()
}

// Close closes the underlying SQLite3 database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
