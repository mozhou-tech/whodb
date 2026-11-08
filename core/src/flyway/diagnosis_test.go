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
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/source"
)

func TestLiveFlywayRecovery(t *testing.T) {
	binary := os.Getenv("WHODB_TEST_FLYWAY_BINARY")
	if binary == "" {
		t.Skip("set WHODB_TEST_FLYWAY_BINARY for disposable H2 recovery verification")
	}
	root := t.TempDir()
	scripts := filepath.Join(root, "sql")
	if err := os.Mkdir(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(scripts, "V1__recover.sql")
	if err := os.WriteFile(script, []byte("INVALID SQL;"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	req := ExecuteRequest{Directory: scripts, TargetDatabase: "disposable_h2"}
	run := func(ctx context.Context, req ExecuteRequest) ExecuteResult {
		preview := InspectDirectory(req.Directory)
		staged, err := prepareMigrationDirectory(preview)
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(staged)
		args := []string{"-url=jdbc:h2:" + filepath.Join(root, "db"), "-locations=filesystem:" + staged}
		if req.outputJSON {
			args = append(args, "-outputType=json")
		}
		if req.skipCallbacks {
			options, err := recoveryCommandOptions(staged)
			if err != nil {
				t.Fatal(err)
			}
			args = append(args, options...)
		}
		args = append(args, req.Action)
		cmd := exec.CommandContext(ctx, binary, args...)
		if req.skipCallbacks {
			cmd.Dir = staged
			cmd.Env = recoveryCommandEnv(os.Environ())
		}
		output, runErr := cmd.CombinedOutput()
		result := ExecuteResult{Success: runErr == nil, Action: req.Action, Output: string(output)}
		if runErr != nil {
			result.Error = runErr.Error()
		}
		return result
	}
	req.Action = "migrate"
	if initial := run(ctx, req); initial.Success {
		t.Fatal("broken SQL did not fail")
	}
	if err := os.WriteFile(script, []byte("CREATE TABLE recovered (id INT);"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, callback := range []string{"beforeInfo.sql", "beforeValidate.sql", "beforeRepair.sql", "beforeMigrate.sql"} {
		if err := os.WriteFile(filepath.Join(scripts, callback), []byte("INVALID CALLBACK SQL;"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FLYWAY_CALLBACKS", "invalid.CallbackMustNotRun")
	t.Setenv("FLYWAY_IGNORE_MIGRATION_PATTERNS", "*:failed")
	diagnosis, snapshot := diagnose(ctx, req, run)
	if diagnosis.Healthy || !diagnosis.RepairAllowed {
		t.Fatalf("real failed migration diagnosis: %+v", diagnosis)
	}
	result := repair(ctx, req, saveTestPlan(snapshot, time.Now().Add(time.Minute)), true, run)
	if !result.Success {
		last := result.Steps[len(result.Steps)-1]
		t.Fatalf("real recovery failed at %s: %s\n%.2000s", last.Action, result.Error, last.Output)
	}
	healthy, _ := diagnose(ctx, req, run)
	if !healthy.Healthy || healthy.RepairAllowed {
		t.Fatalf("repaired migration is not healthy: %+v", healthy)
	}
}

func diagnosticFixture(t *testing.T) (ExecuteRequest, migrationExecutor, *[]string, *bool) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "V1__bad.sql"), []byte("CREATE TABLE example (id INT);"), 0600); err != nil {
		t.Fatal(err)
	}
	req := ExecuteRequest{Directory: root, TargetDatabase: "example", Credentials: &source.Credentials{SourceType: "MySQL"}, ValidateOnMigrate: true}
	calls := []string{}
	repaired := false
	run := func(_ context.Context, request ExecuteRequest) ExecuteResult {
		calls = append(calls, request.Action)
		if !request.skipCallbacks {
			t.Error("diagnostic/repair operations must disable callbacks")
		}
		result := ExecuteResult{Success: true, Action: request.Action}
		var output any
		switch request.Action {
		case "info":
			output = map[string]any{"migrations": []map[string]any{{"version": "1", "state": "Failed", "filepath": "/temporary/V1__bad.sql"}}}
		case "validate":
			if repaired {
				output = map[string]any{"validationSuccessful": true}
			} else {
				output = map[string]any{"validationSuccessful": false, "invalidMigrations": []map[string]any{{"version": "1", "filepath": "/temporary/V1__bad.sql", "errorDetails": map[string]string{"errorCode": "FAILED_VERSIONED_MIGRATION", "errorMessage": "failed migration"}}}}
			}
		case "repair":
			repaired = true
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		result.Output = string(encoded)
		return result
	}
	return req, run, &calls, &repaired
}

func saveTestPlan(snapshot [32]byte, expires time.Time) string {
	token := time.Now().String()
	repairPlans.Lock()
	repairPlans.plans[token] = repairPlan{snapshot: snapshot, expires: expires}
	repairPlans.Unlock()
	return token
}

func TestDiagnosisAndConfirmedRepair(t *testing.T) {
	req, run, calls, _ := diagnosticFixture(t)
	diagnosis, snapshot := diagnose(context.Background(), req, run)
	if diagnosis.Healthy || !diagnosis.RepairAllowed || len(diagnosis.Issues) != 1 || diagnosis.Issues[0].SQL == "" {
		t.Fatalf("unexpected diagnosis: %+v", diagnosis)
	}
	if !reflect.DeepEqual(*calls, []string{"info", "validate"}) {
		t.Fatalf("diagnosis changed data: %v", *calls)
	}
	token := saveTestPlan(snapshot, time.Now().Add(time.Minute))
	*calls = nil
	result := repair(context.Background(), req, token, false, run)
	if result.Success || len(*calls) != 0 {
		t.Fatal("repair ran without confirmation")
	}
	result = repair(context.Background(), req, token, true, run)
	if !result.Success || len(result.Steps) != 3 {
		t.Fatalf("unexpected repair: %+v", result)
	}
	if !reflect.DeepEqual(*calls, []string{"info", "validate", "repair", "validate", "migrate"}) {
		t.Fatalf("wrong workflow: %v", *calls)
	}
	*calls = nil
	if repeated := repair(context.Background(), req, token, true, run); repeated.Success || len(*calls) != 0 {
		t.Fatal("single-use plan was replayed")
	}
}

func TestRepairRejectsChangedOrExpiredPlans(t *testing.T) {
	for _, change := range []string{"database", "script", "expired", "schema", "credentials", "history"} {
		t.Run(change, func(t *testing.T) {
			req, run, calls, _ := diagnosticFixture(t)
			_, snapshot := diagnose(context.Background(), req, run)
			expires := time.Now().Add(time.Minute)
			switch change {
			case "database":
				req.TargetDatabase = "other"
			case "schema":
				req.TargetSchema = "other"
			case "credentials":
				req.Credentials.SourceType = "Postgres"
			case "script":
				if err := os.WriteFile(filepath.Join(req.Directory, "V1__bad.sql"), []byte("SELECT 1;"), 0600); err != nil {
					t.Fatal(err)
				}
			case "expired":
				expires = time.Now().Add(-time.Minute)
			}
			*calls = nil
			recheck := run
			if change == "history" {
				recheck = func(ctx context.Context, req ExecuteRequest) ExecuteResult {
					result := run(ctx, req)
					if req.Action == "info" {
						result.Output = `{"migrations":[{"version":"1","state":"Failed","filepath":"V1__bad.sql","installedOnUTC":"changed"}]}`
					}
					return result
				}
			}
			result := repair(context.Background(), req, saveTestPlan(snapshot, expires), true, recheck)
			if result.Success {
				t.Fatal("stale plan accepted")
			}
			for _, action := range *calls {
				if action == "repair" || action == "migrate" {
					t.Fatalf("stale plan executed %s", action)
				}
			}
		})
	}
}

func TestRepairStopsAtFirstFailure(t *testing.T) {
	for _, failure := range []string{"repair", "validate", "migrate"} {
		t.Run(failure, func(t *testing.T) {
			req, run, calls, repaired := diagnosticFixture(t)
			_, snapshot := diagnose(context.Background(), req, run)
			*calls = nil
			failing := func(ctx context.Context, req ExecuteRequest) ExecuteResult {
				isPostRepair := *repaired
				result := run(ctx, req)
				if req.Action == failure && (failure != "validate" || isPostRepair) {
					if failure == "validate" {
						result.Output = `{"validationSuccessful":false}`
					} else {
						result.Success, result.Error = false, "failed"
					}
				}
				return result
			}
			result := repair(context.Background(), req, saveTestPlan(snapshot, time.Now().Add(time.Minute)), true, failing)
			if result.Success {
				t.Fatal("workflow continued after failure")
			}
			if result.Steps[len(result.Steps)-1].Action != failure {
				t.Fatalf("wrong final step: %+v", result.Steps)
			}
			migrates := 0
			for _, action := range *calls {
				if action == "migrate" {
					migrates++
				}
			}
			if migrates > 1 || (failure != "migrate" && migrates != 0) {
				t.Fatal("unsafe migration retry")
			}
		})
	}
}

func TestDiagnosisBlocksOtherValidationConflicts(t *testing.T) {
	for _, code := range []string{"CHECKSUM_MISMATCH", "APPLIED_VERSIONED_MIGRATION_NOT_RESOLVED", "FAILED_REPEATABLE_MIGRATION"} {
		t.Run(code, func(t *testing.T) {
			req, run, _, _ := diagnosticFixture(t)
			wrapper := func(ctx context.Context, req ExecuteRequest) ExecuteResult {
				result := run(ctx, req)
				if req.Action == "validate" {
					result.Output = `{"validationSuccessful":false,"invalidMigrations":[{"version":"1","filepath":"V1__bad.sql","errorDetails":{"errorCode":"` + code + `"}}]}`
				}
				return result
			}
			result, _ := diagnose(context.Background(), req, wrapper)
			if result.RepairAllowed || result.Healthy {
				t.Fatalf("conflict was eligible for repair: %+v", result)
			}
		})
	}
}

func TestRepairUsesReviewedFilesThroughout(t *testing.T) {
	req, run, _, _ := diagnosticFixture(t)
	_, snapshot := diagnose(context.Background(), req, run)
	originalFile := filepath.Join(req.Directory, "V1__bad.sql")
	wrapped := func(ctx context.Context, current ExecuteRequest) ExecuteResult {
		if current.Action == "repair" {
			if err := os.WriteFile(originalFile, []byte("DROP TABLE example;"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		content, err := os.ReadFile(filepath.Join(current.Directory, "V1__bad.sql"))
		if err != nil || string(content) != "CREATE TABLE example (id INT);" {
			t.Fatalf("unreviewed SQL reached %s", current.Action)
		}
		return run(ctx, current)
	}
	result := repair(context.Background(), req, saveTestPlan(snapshot, time.Now().Add(time.Minute)), true, wrapped)
	if !result.Success {
		t.Fatalf("frozen recovery failed: %+v", result)
	}
}

func TestStructuralSnapshotChangesBlockRepair(t *testing.T) {
	req, run, calls, _ := diagnosticFixture(t)
	fingerprint := "before"
	req.RecoveryPlanner = func(context.Context, string) (*engine.MigrationRecoveryPlan, error) {
		return &engine.MigrationRecoveryPlan{Safe: true, Fingerprint: fingerprint, Matched: 1, Steps: []engine.MigrationRecoveryStep{}}, nil
	}
	diagnosis, snapshot := diagnose(context.Background(), req, run)
	if !diagnosis.RepairAllowed {
		t.Fatal("initial structural plan blocked")
	}
	fingerprint = "changed"
	*calls = nil
	result := repair(context.Background(), req, saveTestPlan(snapshot, time.Now().Add(time.Minute)), true, run)
	if result.Success || !reflect.DeepEqual(*calls, []string{"info", "validate"}) {
		t.Fatalf("changed schema was applied: %+v, %v", result, *calls)
	}
}

func TestStructuralApprovalCannotBypassManualCleanup(t *testing.T) {
	req, run, calls, _ := diagnosticFixture(t)
	_, snapshot := diagnose(context.Background(), req, run)
	req.StructureRecoveryConfirmed = true
	*calls = nil
	result := repair(context.Background(), req, saveTestPlan(snapshot, time.Now().Add(time.Minute)), false, run)
	if result.Success || !reflect.DeepEqual(*calls, []string{"info", "validate"}) {
		t.Fatalf("structural-only approval bypassed missing verifier: %+v, %v", result, *calls)
	}
}

func TestStructuralRecoveryChecksExpectedChecksumOnly(t *testing.T) {
	for _, version := range []string{"1", "other"} {
		t.Run(version, func(t *testing.T) {
			req, run, _, _ := diagnosticFixture(t)
			migrated, restored, repairCount := false, false, 0
			req.RecoveryPlanner = func(context.Context, string) (*engine.MigrationRecoveryPlan, error) {
				if migrated {
					return &engine.MigrationRecoveryPlan{Safe: true, Matched: 2}, nil
				}
				return &engine.MigrationRecoveryPlan{Safe: true, Fingerprint: "same", Matched: 1, Pending: 1, RemainingSQL: "ALTER TABLE example ADD added INT;", Steps: []engine.MigrationRecoveryStep{}}, nil
			}
			_, snapshot := diagnose(context.Background(), req, run)
			wrapper := func(ctx context.Context, current ExecuteRequest) ExecuteResult {
				result := run(ctx, current)
				if current.Action == "repair" {
					repairCount++
					if migrated {
						restored = true
					}
				}
				if current.Action == "migrate" {
					if current.migrationTarget != "1" {
						t.Fatal("recovery did not restrict the migration target")
					}
					content, err := os.ReadFile(filepath.Join(current.Directory, "V1__bad.sql"))
					if err != nil || string(content) != "ALTER TABLE example ADD added INT;" {
						t.Fatal("recovery did not use the reviewed remaining SQL")
					}
					migrated = true
				}
				if current.Action == "validate" && migrated && !restored {
					result.Output = `{"validationSuccessful":false,"invalidMigrations":[{"version":"` + version + `","errorDetails":{"errorCode":"CHECKSUM_MISMATCH"}}]}`
				}
				return result
			}
			result := repair(context.Background(), req, saveTestPlan(snapshot, time.Now().Add(time.Minute)), true, wrapper)
			if version == "1" {
				if !result.Success || !restored || repairCount != 2 {
					t.Fatalf("expected checksum not restored: %+v", result)
				}
			} else if result.Success || restored || repairCount != 1 {
				t.Fatalf("unrelated checksum was repaired: %+v", result)
			}
		})
	}
}
