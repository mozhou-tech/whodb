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

package flyway

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/clidey/whodb/core/src/engine"
	mysqlplugin "github.com/clidey/whodb/core/src/plugins/mysql"
	"github.com/clidey/whodb/core/src/source"
	"github.com/clidey/whodb/core/src/sqlident"
	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestLiveMySQLStructuralRecovery(t *testing.T) {
	addr := os.Getenv("WHODB_TEST_RECOVERY_MYSQL_ADDR")
	if addr == "" || os.Getenv("WHODB_TEST_FLYWAY_BINARY") == "" {
		t.Skip("requires disposable MySQL and installed Flyway")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
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
	defer admin.Close()
	database := fmt.Sprintf("whodb_flyway_test_%d", time.Now().UnixNano())
	identifier, err := sqlident.New("", database)
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := identifier.SQL(sqlident.Backtick)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quoted+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.ExecContext(context.Background(), "DROP DATABASE "+quoted) }()
	config.DBName = database
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	profileSQL := "CREATE TABLE profile (id BIGINT NOT NULL, name VARCHAR(64) NULL, PRIMARY KEY (id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;"
	seed := profileSQL + "INSERT INTO profile VALUES(1,'preserve'); CREATE TABLE users(id BIGINT NOT NULL PRIMARY KEY, tenant VARCHAR(64) NOT NULL, account VARCHAR(64) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4; INSERT INTO users VALUES(1,'tenant','keep');"
	if _, err := db.ExecContext(ctx, seed); err != nil {
		t.Fatal(err)
	}
	originalSQL := profileSQL + "\nALTER TABLE users ADD COLUMN identity_type VARCHAR(8) NOT NULL DEFAULT 'HUMAN';\nCREATE INDEX ix_identity ON users(tenant,identity_type,id);\nCREATE UNIQUE INDEX ux_account ON users(tenant,account);"
	dir := t.TempDir()
	script := filepath.Join(dir, "V20260908_1__structure.sql")
	if err := os.WriteFile(script, []byte(originalSQL), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "V20990101__future.sql"), []byte("CREATE TABLE unexpected_future (id INT);"), 0600); err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"Hostname": host, "Port": port, "Username": config.User, "Password": config.Passwd, "Database": database}
	req := ExecuteRequest{Credentials: &source.Credentials{SourceType: "MySQL", Values: values}, TargetDatabase: database, Directory: dir, Action: "migrate", BaselineOnMigrate: true, ValidateOnMigrate: true}
	initial := Execute(ctx, req)
	if initial.Success || !strings.Contains(initial.Output, "already exists") {
		t.Fatalf("did not reproduce existing-table failure: %s", initial.Output)
	}
	plugin := mysqlplugin.NewMySQLPlugin()
	req.RecoveryPlanner = func(ctx context.Context, sql string) (*engine.MigrationRecoveryPlan, error) {
		return plugin.PlanMigrationRecovery(&engine.PluginConfig{Context: ctx, Credentials: &engine.Credentials{Type: "MySQL", Hostname: host, Username: config.User, Password: config.Passwd, Database: database, Advanced: []engine.Record{{Key: "Port", Value: port}}}}, "", sql)
	}
	diagnosis := Diagnose(ctx, req)
	if !diagnosis.RepairAllowed || diagnosis.Token == "" || len(diagnosis.Issues) != 1 || diagnosis.Issues[0].Recovery == nil {
		t.Fatalf("unexpected diagnosis: %+v", diagnosis)
	}
	plan := diagnosis.Issues[0].Recovery
	if !plan.Safe || plan.Matched != 1 || plan.Pending != 3 {
		t.Fatalf("unexpected structure plan: %+v", plan)
	}
	req.StructureRecoveryConfirmed = true
	result := Repair(ctx, req, diagnosis.Token, false)
	if !result.Success {
		last := result.Steps[len(result.Steps)-1]
		t.Fatalf("recovery failed at %s: %s\n%.3000s", last.Action, result.Error, last.Output)
	}
	actions := []string{}
	for _, step := range result.Steps {
		actions = append(actions, step.Action)
	}
	want := []string{"repair", "validate", "migrate", "verify-structure", "verify-original-checksum", "restore-checksum", "validate-final"}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("wrong recovery steps: %v", actions)
	}
	var preserved, account, identity string
	if err := db.QueryRowContext(ctx, "SELECT name FROM profile WHERE id=1").Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT account,identity_type FROM users WHERE id=1").Scan(&account, &identity); err != nil {
		t.Fatal(err)
	}
	if preserved != "preserve" || account != "keep" || identity != "HUMAN" {
		t.Fatalf("existing data changed: %s %s %s", preserved, account, identity)
	}
	content, err := os.ReadFile(script)
	if err != nil || string(content) != originalSQL {
		t.Fatal("original migration file was changed")
	}
	if healthy := Diagnose(ctx, req); !healthy.Healthy {
		t.Fatalf("original checksums do not validate: %+v", healthy)
	}
	var future int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME='unexpected_future'", database).Scan(&future); err != nil || future != 0 {
		t.Fatalf("recovery executed an unrelated future migration: %d, %v", future, err)
	}

	// A later failed version containing only already-applied DDL must become a
	// verified no-op, with the original file checksum restored afterward.
	second := filepath.Join(dir, "V20260909_1__already_present.sql")
	if err := os.WriteFile(second, []byte(originalSQL), 0600); err != nil {
		t.Fatal(err)
	}
	if failed := Execute(ctx, req); failed.Success {
		t.Fatal("second existing-table failure was not reproduced")
	}
	complete := Diagnose(ctx, req)
	if !complete.RepairAllowed || len(complete.Issues) != 1 || complete.Issues[0].Recovery == nil || complete.Issues[0].Recovery.Matched != 4 || complete.Issues[0].Recovery.Pending != 0 {
		t.Fatalf("all-present recovery plan incorrect: %+v", complete)
	}
	if result := Repair(ctx, req, complete.Token, false); !result.Success {
		t.Fatalf("all-present recovery failed: %s", result.Error)
	}
	if final := Diagnose(ctx, req); !final.Healthy {
		t.Fatalf("all-present original checksum failed: %+v", final)
	}
}
