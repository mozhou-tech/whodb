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

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/pingcap/tidb/pkg/parser/ast"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/sqlident"
)

func definitionForTest(t *testing.T, sql string) migrationTable {
	t.Helper()
	nodes, err := parseMigrationSQL(sql)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := migrationTableDefinition(nodes[0].(*ast.CreateTableStmt), migrationDefaults{Engine: "innodb", Charset: "utf8mb4", Collation: "utf8mb4_0900_ai_ci", CharsetCollation: "utf8mb4_0900_ai_ci", CharsetDefaults: map[string]string{"utf8mb4": "utf8mb4_0900_ai_ci", "binary": "binary"}})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestMigrationDefinitionNormalizesMySQLMetadata(t *testing.T) {
	written := definitionForTest(t, "CREATE TABLE example (id BIGINT NOT NULL, name VARCHAR(64) NULL, version BIGINT NOT NULL DEFAULT 0, PRIMARY KEY (id), KEY ix_name(name)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	actual := definitionForTest(t, "CREATE TABLE `example` (`id` bigint NOT NULL, `name` varchar(64) DEFAULT NULL, `version` bigint NOT NULL DEFAULT '0', PRIMARY KEY (`id`), KEY `ix_name` (`name`)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci")
	if !reflect.DeepEqual(written, actual) {
		t.Fatalf("equivalent definitions differ:\n%+v\n%+v", written, actual)
	}
	other := definitionForTest(t, "CREATE TABLE example (id BIGINT NOT NULL, name VARCHAR(32) NULL, version BIGINT NOT NULL DEFAULT 0, PRIMARY KEY (id), KEY ix_name(name)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	if reflect.DeepEqual(written, other) {
		t.Fatal("different lengths considered equivalent")
	}
}

func TestMigrationParserRejectsUnreviewedSyntax(t *testing.T) {
	for _, sql := range []string{"/*! DROP TABLE users */", "CREATE TABLE ${table} (id INT)", "DELIMITER $$ CREATE PROCEDURE x() SELECT 1 $$"} {
		if _, err := parseMigrationSQL(sql); err == nil {
			t.Fatalf("accepted unsupported SQL: %s", sql)
		}
	}
}

func TestMigrationRowFormatRequirements(t *testing.T) {
	base := "CREATE TABLE example (id BIGINT NOT NULL, PRIMARY KEY(id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	actual := definitionForTest(t, base+" ROW_FORMAT=DYNAMIC")
	planner := &mysqlMigrationPlanner{futureColumns: map[string]map[string]bool{}, futureIndexes: map[string]map[string]bool{}}
	for _, suffix := range []string{"", " ROW_FORMAT=DEFAULT", " ROW_FORMAT=DYNAMIC"} {
		expected := definitionForTest(t, base+suffix)
		if reason := planner.createConflict("example", actual, expected); reason != "" {
			t.Fatalf("compatible storage format rejected: %s", reason)
		}
	}
	explicit := definitionForTest(t, base+" ROW_FORMAT=COMPACT")
	if reason := planner.createConflict("example", actual, explicit); !strings.Contains(reason, "ROW_FORMAT") {
		t.Fatalf("explicit mismatch was ignored: %s", reason)
	}
}

func TestMigrationColumnCharsetComparison(t *testing.T) {
	base := "CREATE TABLE example (id INT NOT NULL PRIMARY KEY, name VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	expected := definitionForTest(t, base)
	actual := definitionForTest(t, base+" ROW_FORMAT=DYNAMIC")
	planner := &mysqlMigrationPlanner{futureColumns: map[string]map[string]bool{}, futureIndexes: map[string]map[string]bool{}}
	if reason := planner.createConflict("example", actual, expected); reason != "" {
		t.Fatalf("matching explicit charset rejected: %s", reason)
	}
	other := definitionForTest(t, strings.Replace(base, "utf8mb4_general_ci", "utf8mb4_0900_ai_ci", 1))
	if reason := planner.createConflict("example", actual, other); !strings.Contains(reason, "name") {
		t.Fatalf("column collation difference ignored: %s", reason)
	}
}

func TestMigrationMultiSpecAlterAndFutureMiddleColumns(t *testing.T) {
	expected := definitionForTest(t, "CREATE TABLE open_api_key (f_id VARCHAR(64) NOT NULL, f_policy_id VARCHAR(64) NULL, f_created_by VARCHAR(64) NOT NULL, PRIMARY KEY (f_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	actual := definitionForTest(t, "CREATE TABLE open_api_key (f_id VARCHAR(64) NOT NULL, f_policy_id VARCHAR(64) NULL, f_rate_limit INT NULL, f_bound_user_id VARCHAR(64) NULL, f_created_by VARCHAR(64) NOT NULL, PRIMARY KEY (f_id), KEY idx_bound_user (f_bound_user_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	planner := &mysqlMigrationPlanner{
		defaults:      migrationDefaults{Engine: "innodb", Charset: "utf8mb4", Collation: "utf8mb4_0900_ai_ci", CharsetCollation: "utf8mb4_0900_ai_ci", CharsetDefaults: map[string]string{"utf8mb4": "utf8mb4_0900_ai_ci"}},
		tables:        map[string]*migrationTableState{"open_api_key": {Definition: actual, Exists: true, OriginalColumns: map[string]bool{"f_id": true, "f_policy_id": true, "f_rate_limit": true, "f_bound_user_id": true, "f_created_by": true}}},
		futureColumns: map[string]map[string]bool{},
		futureIndexes: map[string]map[string]bool{},
		plan:          &engine.MigrationRecoveryPlan{Safe: true, Steps: []engine.MigrationRecoveryStep{}},
	}
	futureNodes, err := parseMigrationSQL("ALTER TABLE open_api_key ADD COLUMN f_rate_limit INT NULL AFTER f_policy_id; ALTER TABLE open_api_key ADD COLUMN f_bound_user_id VARCHAR(64) NULL AFTER f_rate_limit, ADD KEY idx_bound_user (f_bound_user_id);")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range futureNodes {
		planner.recordFutureAdditions(node)
	}
	if reason := planner.createConflict("open_api_key", actual, expected); reason != "" {
		t.Fatalf("future middle column or index rejected: %s", reason)
	}
	for _, node := range futureNodes {
		if err := planner.inspect(node); err != nil {
			t.Fatalf("multi-spec alter inspect failed: %v", err)
		}
	}
	if planner.plan.Matched != 3 || planner.plan.Pending != 0 {
		t.Fatalf("expected 3 matched steps and 0 pending, got matched=%d pending=%d", planner.plan.Matched, planner.plan.Pending)
	}
}

func recoveryTestDatabase(t *testing.T) (*sql.DB, *engine.PluginConfig) {
	t.Helper()
	addr := os.Getenv("WHODB_TEST_RECOVERY_MYSQL_ADDR")
	if addr == "" {
		t.Skip("set WHODB_TEST_RECOVERY_MYSQL_ADDR for a disposable local MySQL server")
	}
	config := mysqldriver.NewConfig()
	config.User = "root"
	config.Passwd = "whodb-recovery-local"
	config.Net = "tcp"
	config.Addr = addr
	config.MultiStatements = true
	admin, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("whodb_recovery_test_%d", time.Now().UnixNano())
	identifier, err := sqlident.New("", name)
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := identifier.SQL(sqlident.Backtick)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+quoted+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP DATABASE "+quoted) })
	config.DBName = name
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	return db, &engine.PluginConfig{Context: context.Background(), Credentials: &engine.Credentials{Type: "MySQL", Hostname: host, Username: config.User, Password: config.Passwd, Database: name, Advanced: []engine.Record{{Key: "Port", Value: port}}}}
}

const recoveryProfileSQL = "CREATE TABLE profile (id BIGINT NOT NULL, name VARCHAR(64) NULL, version BIGINT NOT NULL DEFAULT 0, PRIMARY KEY (id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;"
const recoveryUserSQL = "CREATE TABLE users (id BIGINT NOT NULL PRIMARY KEY, tenant VARCHAR(64) NOT NULL, account VARCHAR(64) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4; INSERT INTO users VALUES (1,'tenant','keep');"
const recoveryScriptSQL = recoveryProfileSQL + "\nALTER TABLE users ADD COLUMN identity_type VARCHAR(8) NOT NULL DEFAULT 'HUMAN';\nCREATE INDEX ix_identity ON users(tenant,identity_type,id);\nCREATE UNIQUE INDEX ux_account ON users(tenant,account);"

func TestLiveMigrationRecoveryPlan(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	if _, err := db.ExecContext(context.Background(), recoveryProfileSQL+recoveryUserSQL); err != nil {
		t.Fatal(err)
	}
	plugin := NewMySQLPlugin()
	plan, err := plugin.PlanMigrationRecovery(config, "", recoveryScriptSQL)
	if err != nil || !plan.Safe || plan.Matched != 1 || plan.Pending != 3 {
		t.Fatalf("unexpected initial plan: %+v, %v", plan, err)
	}
	var account string
	if err := db.QueryRowContext(context.Background(), "SELECT account FROM users WHERE id=1").Scan(&account); err != nil || account != "keep" {
		t.Fatalf("existing data changed: %s, %v", account, err)
	}
	if strings.Contains(plan.RemainingSQL, "CREATE TABLE") {
		t.Fatal("matched table was not skipped")
	}
	if _, err := db.ExecContext(context.Background(), plan.RemainingSQL); err != nil {
		t.Fatal(err)
	}
	complete, err := plugin.PlanMigrationRecovery(config, "", recoveryScriptSQL)
	if err != nil || !complete.Safe || complete.Matched != 4 || complete.Pending != 0 {
		t.Fatalf("unexpected complete plan: %+v, %v", complete, err)
	}
	if complete.Fingerprint == plan.Fingerprint {
		t.Fatal("schema changes did not change the fingerprint")
	}
	if err := db.QueryRowContext(context.Background(), "SELECT account FROM users WHERE id=1").Scan(&account); err != nil || account != "keep" {
		t.Fatalf("recovery lost data: %s, %v", account, err)
	}
}

func TestLiveMigrationRecoveryBlocksConflicts(t *testing.T) {
	for _, scenario := range []string{"table", "column", "index", "duplicates", "destructive"} {
		t.Run(scenario, func(t *testing.T) {
			db, config := recoveryTestDatabase(t)
			seed := recoveryProfileSQL + recoveryUserSQL
			switch scenario {
			case "table":
				seed = strings.Replace(seed, "name VARCHAR(64)", "name VARCHAR(32)", 1)
			case "column":
				seed += "ALTER TABLE users ADD identity_type VARCHAR(16) NOT NULL DEFAULT 'HUMAN';"
			case "index":
				seed += "ALTER TABLE users ADD identity_type VARCHAR(8) NOT NULL DEFAULT 'HUMAN'; CREATE INDEX ix_identity ON users(id,tenant,identity_type);"
			case "duplicates":
				seed += "INSERT INTO users VALUES (2,'tenant','keep');"
			}
			if _, err := db.ExecContext(context.Background(), seed); err != nil {
				t.Fatal(err)
			}
			script := recoveryScriptSQL
			if scenario == "destructive" {
				script += "DROP TABLE users;"
			}
			plan, err := NewMySQLPlugin().PlanMigrationRecovery(config, "", script)
			if err != nil || plan.Safe || plan.Error == "" {
				t.Fatalf("conflict accepted: %+v, %v", plan, err)
			}
			var account string
			if err := db.QueryRowContext(context.Background(), "SELECT account FROM users WHERE id=1").Scan(&account); err != nil || account != "keep" {
				t.Fatalf("diagnosis modified data: %s, %v", account, err)
			}
		})
	}
}

func TestLiveRecoveryRecognizesLaterAdditions(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	script := recoveryProfileSQL + "ALTER TABLE profile ADD COLUMN extra VARCHAR(8) NULL; CREATE INDEX ix_extra ON profile(extra);"
	if _, err := db.ExecContext(context.Background(), script+"INSERT INTO profile VALUES (1,'keep',0,'value');"); err != nil {
		t.Fatal(err)
	}
	plan, err := NewMySQLPlugin().PlanMigrationRecovery(config, "", script)
	if err != nil || !plan.Safe || plan.Matched != 3 || plan.Pending != 0 {
		t.Fatalf("later additions caused a false conflict: %+v, %v", plan, err)
	}
}

func TestLiveRecoveryQuotesObjectNames(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	name := "profile`; DROP TABLE users;--"
	identifier, err := sqlident.New("", name)
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := identifier.SQL(sqlident.Backtick)
	if err != nil {
		t.Fatal(err)
	}
	script := "CREATE TABLE " + quoted + " (id INT NOT NULL PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;"
	if _, err := db.ExecContext(context.Background(), recoveryUserSQL+script); err != nil {
		t.Fatal(err)
	}
	plan, err := NewMySQLPlugin().PlanMigrationRecovery(config, "", script)
	if err != nil || !plan.Safe || plan.Matched != 1 {
		t.Fatalf("quoted name failed: %+v, %v", plan, err)
	}
	var account string
	if err := db.QueryRowContext(context.Background(), "SELECT account FROM users WHERE id=1").Scan(&account); err != nil || account != "keep" {
		t.Fatalf("guard table changed: %s, %v", account, err)
	}
}

func TestLiveRecoveryJSONCompositeKeys(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	script := "CREATE TABLE bot (f_id VARCHAR(64) NOT NULL, f_tenant_id VARCHAR(64) NOT NULL, f_payload JSON NULL, f_version BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(f_tenant_id,f_id), KEY ix_id(f_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;"
	if _, err := db.ExecContext(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	plan, err := NewMySQLPlugin().PlanMigrationRecovery(config, "", script)
	if err != nil || !plan.Safe || plan.Matched != 1 {
		t.Fatalf("JSON/composite schema failed: %+v, %v", plan, err)
	}
}

func TestLiveRecoveryChecksExplicitCharsetDefaults(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	identifier, err := sqlident.New("", config.Credentials.Database)
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := identifier.SQL(sqlident.Backtick)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "ALTER DATABASE "+quoted+" COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	unicodeSQL := strings.Replace(recoveryProfileSQL, "DEFAULT CHARSET=utf8mb4", "DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci", 1)
	if _, err := db.ExecContext(context.Background(), unicodeSQL); err != nil {
		t.Fatal(err)
	}
	plan, err := NewMySQLPlugin().PlanMigrationRecovery(config, "", recoveryProfileSQL)
	if err != nil || plan.Safe {
		t.Fatalf("database default hid a table collation difference: %+v, %v", plan, err)
	}
	plan, err = NewMySQLPlugin().PlanMigrationRecovery(config, "", unicodeSQL)
	if err != nil || !plan.Safe || plan.Matched != 1 {
		t.Fatalf("explicit matching collation failed: %+v, %v", plan, err)
	}
}

func TestLiveRecoveryUniqueIndexAllowsNulls(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	if _, err := db.ExecContext(context.Background(), "CREATE TABLE nullable_keys (tenant INT NULL, account VARCHAR(20) NULL); INSERT INTO nullable_keys VALUES (1,NULL),(1,NULL);"); err != nil {
		t.Fatal(err)
	}
	plan, err := NewMySQLPlugin().PlanMigrationRecovery(config, "", "CREATE UNIQUE INDEX ux_nullable ON nullable_keys(tenant,account);")
	if err != nil || !plan.Safe || plan.Pending != 1 {
		t.Fatalf("nullable unique keys were incorrectly blocked: %+v, %v", plan, err)
	}
	if _, err := db.ExecContext(context.Background(), plan.RemainingSQL); err != nil {
		t.Fatal(err)
	}
}

func TestLiveRecoveryExistingStorageAndColumnOptions(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			db, config := recoveryTestDatabase(t)
			seed := strings.Replace(recoveryUserSQL, "account VARCHAR(64) NOT NULL", "account VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL", 1)
			seed = strings.Replace(seed, "DEFAULT CHARSET=utf8mb4;", "DEFAULT CHARSET=utf8mb4 ROW_FORMAT=DYNAMIC;", 1)
			if duplicate {
				seed += "INSERT INTO users VALUES(2,'tenant','KEEP');"
			}
			if _, err := db.ExecContext(context.Background(), recoveryProfileSQL+seed); err != nil {
				t.Fatal(err)
			}
			plan, err := NewMySQLPlugin().PlanMigrationRecovery(config, "", recoveryScriptSQL)
			if err != nil {
				t.Fatal(err)
			}
			if duplicate {
				if plan.Safe || !strings.Contains(plan.Error, "重复数据") {
					t.Fatalf("expected duplicate conflict after metadata check: %+v", plan)
				}
			} else {
				if !plan.Safe || plan.Matched != 1 || plan.Pending != 3 {
					t.Fatalf("storage/charset metadata incorrectly blocked recovery: %+v", plan)
				}
			}
		})
	}
}

func TestLiveRecoveryDuplicateExport(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	seed := "CREATE TABLE duplicate_rows (id BIGINT NOT NULL PRIMARY KEY, tenant INT NULL, account VARCHAR(30) COLLATE utf8mb4_0900_ai_ci NULL, note TEXT);" +
		"INSERT INTO duplicate_rows VALUES (9007199254740993,1,'Alpha','=1+1'),(9007199254740994,1,'alpha','second'),(3,2,'Beta','third'),(4,2,'beta','fourth'),(5,3,'Alpha','unique'),(6,1,NULL,'null'),(7,1,NULL,'null');"
	if _, err := db.ExecContext(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	plugin := NewMySQLPlugin()
	plan, err := plugin.PlanMigrationRecovery(config, "", "CREATE UNIQUE INDEX ux_duplicate ON duplicate_rows(tenant,account);")
	if err != nil || plan.Safe || len(plan.Steps) != 1 || plan.Steps[0].DuplicateQuery == "" {
		t.Fatalf("duplicate report unavailable: %+v, %v", plan, err)
	}
	rows, err := plugin.RawExecute(config, plan.Steps[0].DuplicateQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 4 || len(rows.Columns) != 4 {
		t.Fatalf("expected four complete duplicate records: %+v", rows)
	}
	ids := map[string]bool{}
	for _, row := range rows.Rows {
		ids[row[0]] = true
	}
	for _, id := range []string{"9007199254740993", "9007199254740994", "3", "4"} {
		if !ids[id] {
			t.Fatalf("record missing or precision lost: %s", id)
		}
	}
	var total int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM duplicate_rows").Scan(&total); err != nil || total != 7 {
		t.Fatalf("export changed records: %d, %v", total, err)
	}
}
