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

package opsplane_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/opsplane"
	"github.com/clidey/whodb/core/src/plugins/clickhouse"
	"github.com/clidey/whodb/core/src/plugins/elasticsearch"
	"github.com/clidey/whodb/core/src/plugins/mysql"
	"github.com/clidey/whodb/core/src/plugins/openobserve"
	"github.com/clidey/whodb/core/src/plugins/postgres"
	"github.com/clidey/whodb/core/src/plugins/redis"
	"github.com/clidey/whodb/core/src/plugins/tdengine"
	"github.com/clidey/whodb/core/src/sourcedial"
	"github.com/clidey/whodb/core/src/types"
)

func toPluginConfig(ctx context.Context, p types.DatabaseCredentials) *engine.PluginConfig {
	adv := []engine.Record{}
	if p.Port != "" {
		adv = append(adv, engine.Record{Key: "Port", Value: p.Port})
	}
	for k, v := range p.Advanced {
		adv = append(adv, engine.Record{Key: k, Value: v})
	}
	cfg := engine.NewPluginConfig(&engine.Credentials{
		Type:      p.Type,
		Hostname:  p.Hostname,
		Username:  p.Username,
		Password:  p.Password,
		Database:  p.Database,
		Advanced:  adv,
		IsProfile: true,
	})
	cfg.Context = ctx
	return cfg
}

func TestLiveOpsControlPlanePluginConnectivity(t *testing.T) {
	if _, err := os.Stat("/Users/mozhou/WorkSpace/mozy/ops-control-plane"); err != nil {
		t.Skip("ops-control-plane not present")
	}

	sourcedial.SetDialer(opsplane.DialContext)
	t.Cleanup(func() {
		opsplane.SetSessionForTesting(nil)
		sourcedial.SetDialer(nil)
	})
	if st := opsplane.GetAuthStatus(); !st.Authenticated {
		opsplane.SetSessionForTesting(&opsplane.AuthStatus{
			Authenticated: true,
			Username:      "admin",
			Role:          "系统管理员",
			AllowAllEnvs:  true,
		})
	} else {
		t.Logf("Using saved session: user=%s endpoint=%s hasToken=%v", st.Username, st.Endpoint, st.Token != "")
	}

	profiles, err := opsplane.RetrieveProfiles()
	if err != nil {
		t.Fatalf("RetrieveProfiles failed: %v", err)
	}

	pluginsByType := map[string]*engine.Plugin{
		string(engine.DatabaseType_Postgres):      postgres.NewPostgresPlugin(),
		string(engine.DatabaseType_MySQL):         mysql.NewMySQLPlugin(),
		string(engine.DatabaseType_Redis):         redis.NewRedisPlugin(),
		string(engine.DatabaseType_TDengine):      tdengine.NewTDenginePlugin(),
		string(engine.DatabaseType_ClickHouse):    clickhouse.NewClickHousePlugin(),
		string(engine.DatabaseType_ElasticSearch): elasticsearch.NewElasticSearchPlugin(),
		string(engine.DatabaseType_OpenObserve):   openobserve.NewOpenObservePlugin(),
	}

	connectedByType := map[string]int{}
	for _, p := range profiles {
		plug, ok := pluginsByType[p.Type]
		if !ok {
			t.Fatalf("missing plugin for profile type %s (%s)", p.Type, p.Alias)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cfg := toPluginConfig(ctx, p)
		okAvail := plug.IsAvailable(ctx, cfg)
		if okAvail && p.Type == string(engine.DatabaseType_TDengine) {
			dbs, err := plug.GetDatabases(cfg)
			if err != nil || len(dbs) == 0 {
				t.Errorf("TDengine GetDatabases failed for %s: dbs=%v err=%v", p.Alias, dbs, err)
			}
			units, err := plug.GetStorageUnits(cfg, p.Database)
			if err != nil {
				t.Errorf("TDengine GetStorageUnits failed for %s: err=%v", p.Alias, err)
			} else if len(units) > 0 {
				targetTable := units[0].Name
				cols, err := plug.GetColumnsForTable(cfg, p.Database, targetTable)
				if err != nil || len(cols) == 0 {
					t.Errorf("TDengine GetColumnsForTable(%s) failed for %s: err=%v", targetTable, p.Alias, err)
				}
				rows, err := plug.GetRows(cfg, &engine.GetRowsRequest{
					Schema:      p.Database,
					StorageUnit: targetTable,
					PageSize:    5,
					PageOffset:  0,
				})
				if err != nil {
					t.Errorf("TDengine GetRows(%s) failed for %s: err=%v", targetTable, p.Alias, err)
				} else {
					t.Logf("  -> TDengine %s table=%s cols=%d rows=%d total=%d", p.Alias, targetTable, len(rows.Columns), len(rows.Rows), rows.TotalCount)
				}
			}
		}
		cancel()

		t.Logf("Profile %-55s type=%-14s db=%-15s available=%v", p.Alias, p.Type, p.Database, okAvail)
		if okAvail {
			connectedByType[p.Type]++
		}
	}

	for _, wantType := range []string{"Postgres", "MySQL", "Redis", "TDengine", "ClickHouse", "OpenObserve"} {
		if connectedByType[wantType] == 0 {
			t.Errorf("expected at least 1 live connected %s profile, got %v", wantType, connectedByType)
		}
	}
}
