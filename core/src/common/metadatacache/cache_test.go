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

package metadatacache

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestStorePersistsAcrossRestartsAndHandlesTTLs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metadata_cache.db")

	store1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store1: %v", err)
	}

	want := []string{"users", "orders", "events"}
	store1.Set("scope-a", "tables:public", want, 50*time.Millisecond, 5*time.Second)

	var got []string
	hit, stale := store1.Get("scope-a", "tables:public", &got)
	if !hit || stale {
		t.Fatalf("expected fresh L1 hit, got hit=%v stale=%v", hit, stale)
	}
	if len(got) != 3 || got[0] != "users" {
		t.Fatalf("unexpected cached payload: %#v", got)
	}

	if err := store1.Close(); err != nil {
		t.Fatalf("failed to close store1: %v", err)
	}

	// Reopen from SQLite3 file (empty L1 memory)
	store2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen store2: %v", err)
	}
	defer func() { _ = store2.Close() }()

	var gotFromDisk []string
	hit, _ = store2.Get("scope-a", "tables:public", &gotFromDisk)
	if !hit || len(gotFromDisk) != 3 {
		t.Fatalf("expected L2 SQLite hit after restart, got hit=%v payload=%#v", hit, gotFromDisk)
	}

	// Wait for soft TTL to expire
	time.Sleep(60 * time.Millisecond)
	var gotStale []string
	hit, stale = store2.Get("scope-a", "tables:public", &gotStale)
	if !hit || !stale {
		t.Fatalf("expected stale hit after soft TTL, got hit=%v stale=%v", hit, stale)
	}

	// InvalidateScope removes entries from both L1 and L2
	store2.InvalidateScope("scope-a")
	var afterInvalidate []string
	if hit, _ := store2.Get("scope-a", "tables:public", &afterInvalidate); hit {
		t.Fatalf("expected cache miss after InvalidateScope")
	}
}

func TestStoreTriggerRefreshDeduplicatesConcurrentCalls(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "metadata_cache.db"))
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	var runs atomic.Int32
	done := make(chan struct{})

	fn := func() {
		runs.Add(1)
		time.Sleep(40 * time.Millisecond)
		close(done)
	}

	store.TriggerRefresh("scope-1", "columns:users", fn)
	store.TriggerRefresh("scope-1", "columns:users", func() {
		runs.Add(1)
	})

	<-done
	if got := runs.Load(); got != 1 {
		t.Fatalf("expected exactly 1 refresh execution, got %d", got)
	}
}
