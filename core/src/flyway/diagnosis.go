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
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/clidey/whodb/core/src/engine"
)

// MigrationIssue describes one migration rejected by Flyway validation.
type MigrationIssue struct {
	Version  string
	Script   string
	Code     string
	Message  string
	SQL      string
	Recovery *engine.MigrationRecoveryPlan
}

// Diagnosis reports read-only validation and an optional single-use repair plan.
type Diagnosis struct {
	Healthy       bool
	RepairAllowed bool
	Token         string
	Issues        []MigrationIssue
	Error         string
}

// RepairResult records each completed step; execution stops at the first failure.
type RepairResult struct {
	Success bool
	Steps   []ExecuteResult
	Error   string
}

type repairPlan struct {
	snapshot [32]byte
	expires  time.Time
}

var repairPlans = struct {
	sync.Mutex
	plans map[string]repairPlan
}{plans: make(map[string]repairPlan)}

type cliMigration struct {
	Version        string `json:"version"`
	Description    string `json:"description"`
	State          string `json:"state"`
	Filepath       string `json:"filepath"`
	InstalledOnUTC string `json:"installedOnUTC"`
	ErrorDetails   struct {
		Code    string `json:"errorCode"`
		Message string `json:"errorMessage"`
	} `json:"errorDetails"`
}

type validationReport struct {
	Valid      *bool           `json:"validationSuccessful"`
	Invalid    []cliMigration  `json:"invalidMigrations"`
	Migrations []cliMigration  `json:"migrations"`
	Error      json.RawMessage `json:"error"`
}

func parseReport(result ExecuteResult) (validationReport, error) {
	var report validationReport
	// Java startup notices may precede the CLI's JSON document.
	start := strings.Index(result.Output, "{")
	if start < 0 {
		return report, errors.New("Flyway 未返回 JSON 诊断报告")
	}
	if err := json.Unmarshal([]byte(result.Output[start:]), &report); err != nil {
		return report, fmt.Errorf("Flyway 诊断报告格式无效：%w", err)
	}
	if len(report.Error) > 0 && string(report.Error) != "null" {
		return report, errors.New("Flyway 无法诊断该数据库，请查看执行日志")
	}
	return report, nil
}

type migrationExecutor func(context.Context, ExecuteRequest) ExecuteResult

func recoveryCommandOptions(directory string) ([]string, error) {
	// Explicit empty configuration prevents user/project Java callbacks and
	// ignored validation conflicts from changing the reviewed recovery plan.
	config := filepath.Join(directory, "whodb-recovery.conf")
	if err := os.WriteFile(config, nil, 0600); err != nil {
		return nil, err
	}
	return []string{"-configFiles=" + config, "-skipDefaultCallbacks=true", "-ignoreMigrationPatterns=*:pending"}, nil
}

func recoveryCommandEnv(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, value := range environment {
		if !strings.HasPrefix(strings.ToUpper(value), "FLYWAY_") {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

const futureMigrationsSeparator = "\n-- WHODB_FUTURE_MIGRATIONS\n"

func plannerSQLForScript(preview DirectoryPreview, script string, sql string) string {
	seen := false
	var future []string
	for _, file := range preview.Files {
		if file.Type != "VERSIONED" {
			continue
		}
		if file.Filename == script {
			seen = true
			continue
		}
		if seen {
			content, err := os.ReadFile(filepath.Join(preview.Directory, file.RelativePath))
			if err == nil && len(content) > 0 {
				future = append(future, string(content))
			}
		}
	}
	if len(future) == 0 {
		return sql
	}
	return sql + futureMigrationsSeparator + strings.Join(future, futureMigrationsSeparator)
}

func diagnose(ctx context.Context, req ExecuteRequest, run migrationExecutor) (Diagnosis, [32]byte) {
	result := Diagnosis{Issues: []MigrationIssue{}}
	preview := InspectDirectory(req.Directory)
	if !preview.Exists || preview.Error != "" || len(preview.Files) == 0 {
		result.Error = "迁移文件不存在或无法读取"
		return result, [32]byte{}
	}
	req.outputJSON, req.skipCallbacks = true, true
	req.Action = "info"
	infoResult := run(ctx, req)
	info, err := parseReport(infoResult)
	if err != nil || !infoResult.Success {
		result.Error = "无法读取 Flyway 迁移历史"
		return result, [32]byte{}
	}
	req.Action = "validate"
	validation, err := parseReport(run(ctx, req))
	if err != nil || validation.Valid == nil {
		result.Error = "无法读取 Flyway 校验结果"
		return result, [32]byte{}
	}
	result.Healthy = *validation.Valid
	if result.Healthy {
		return result, [32]byte{}
	}
	failed := map[string]bool{}
	for i := range info.Migrations {
		migration := &info.Migrations[i]
		if migration.State == "Failed" {
			failed[migration.Version] = true
		}
		// Locations use a different temporary directory for every CLI invocation.
		migration.Filepath = filepath.Base(migration.Filepath)
	}
	allowed := len(validation.Invalid) > 0 && len(failed) > 0
	for _, invalid := range validation.Invalid {
		issue := MigrationIssue{Version: invalid.Version, Script: filepath.Base(invalid.Filepath), Code: invalid.ErrorDetails.Code, Message: invalid.ErrorDetails.Message}
		matched := 0
		for _, file := range preview.Files {
			if file.Filename == issue.Script && file.Type == "VERSIONED" {
				content, readErr := os.ReadFile(filepath.Join(preview.Directory, file.RelativePath))
				if readErr != nil {
					result.Error = "无法读取失败版本的迁移脚本"
					return result, [32]byte{}
				}
				issue.SQL = string(content)
				matched++
			}
		}
		if issue.Code != "FAILED_VERSIONED_MIGRATION" || !failed[issue.Version] || matched != 1 {
			allowed = false
		}
		delete(failed, issue.Version)
		result.Issues = append(result.Issues, issue)
	}
	result.RepairAllowed = allowed && len(failed) == 0
	if result.RepairAllowed && req.RecoveryPlanner != nil {
		for i := range result.Issues {
			issue := &result.Issues[i]
			plan, planErr := req.RecoveryPlanner(ctx, plannerSQLForScript(preview, issue.Script, issue.SQL))
			if errors.Is(planErr, errors.ErrUnsupported) {
				continue
			}
			if planErr != nil {
				result.RepairAllowed, result.Error = false, "无法读取目标数据库结构："+planErr.Error()
				break
			}
			issue.Recovery = plan
			if plan == nil || !plan.Safe {
				result.RepairAllowed = false
			}
			if plan != nil && len(result.Issues) != 1 {
				result.RepairAllowed, result.Error = false, "结构自动补齐目前只支持一个失败版本，请分别处理多个失败版本"
			}
		}
	}
	if !result.RepairAllowed {
		return result, [32]byte{}
	}
	// Bind the plan to credentials, target, migration history, every SQL file,
	// and script configuration. Changes require a fresh user-reviewed diagnosis.
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	reviewedRequest := req
	if req.reviewDirectory != "" {
		reviewedRequest.Directory = req.reviewDirectory
	}
	if err := encoder.Encode(reviewedRequest); err != nil {
		result.Error = err.Error()
		result.RepairAllowed = false
		return result, [32]byte{}
	}
	_ = encoder.Encode(info.Migrations)
	_ = encoder.Encode(result.Issues)
	for _, file := range preview.Files {
		_ = encoder.Encode(file.RelativePath)
		for _, suffix := range []string{"", ".conf"} {
			content, readErr := os.ReadFile(filepath.Join(preview.Directory, file.RelativePath) + suffix)
			if suffix == ".conf" && errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				result.Error, result.RepairAllowed = readErr.Error(), false
				return result, [32]byte{}
			}
			_ = encoder.Encode(content)
		}
	}
	var snapshot [32]byte
	copy(snapshot[:], hash.Sum(nil))
	return result, snapshot
}

// Diagnose reads Flyway info and validation without running SQL callbacks.
// Repair requires explicit approval of a verified schema plan or completed manual cleanup.
func Diagnose(ctx context.Context, req ExecuteRequest) Diagnosis {
	result, snapshot := diagnose(ctx, req, Execute)
	if !result.RepairAllowed {
		return result
	}
	result.Token = rand.Text()
	repairPlans.Lock()
	defer repairPlans.Unlock()
	for token, plan := range repairPlans.plans {
		if time.Now().After(plan.expires) {
			delete(repairPlans.plans, token)
		}
	}
	repairPlans.plans[result.Token] = repairPlan{snapshot: snapshot, expires: time.Now().Add(10 * time.Minute)}
	return result
}

func repair(ctx context.Context, req ExecuteRequest, token string, confirmed bool, run migrationExecutor) RepairResult {
	result := RepairResult{Steps: []ExecuteResult{}}
	if !confirmed && !req.StructureRecoveryConfirmed {
		result.Error = "请先确认已核对修复计划或处理部分执行的残留变更"
		return result
	}
	repairPlans.Lock()
	plan, ok := repairPlans.plans[token]
	delete(repairPlans.plans, token)
	repairPlans.Unlock()
	if !ok || time.Now().After(plan.expires) {
		result.Error = "修复计划已过期或已使用，请重新诊断"
		return result
	}
	preview := InspectDirectory(req.Directory)
	if !preview.Exists || preview.Error != "" {
		result.Error = "迁移文件发生变化或无法读取，请重新诊断"
		return result
	}
	frozen, err := prepareMigrationDirectory(preview)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer func() { _ = os.RemoveAll(frozen) }()
	// Recheck and execute the same private file snapshot, even if source files
	// change while repair or validation is running.
	req.reviewDirectory, req.Directory = req.Directory, frozen
	diagnosis, snapshot := diagnose(ctx, req, run)
	if !diagnosis.RepairAllowed || snapshot != plan.snapshot {
		result.Error = "数据库历史、目标、结构或迁移文件已发生变化，请重新诊断"
		return result
	}
	req.skipCallbacks = true
	req.ValidateOnMigrate = true
	var structural *engine.MigrationRecoveryPlan
	var structuralVersion, structuralFile string
	for _, issue := range diagnosis.Issues {
		if issue.Recovery != nil && issue.Recovery.Safe {
			structural, structuralVersion, structuralFile = issue.Recovery, issue.Version, issue.Script
		}
	}
	if !confirmed && structural == nil {
		result.Error = "当前连接器无法自动核对结构，请先人工处理残留变更后再确认修复"
		return result
	}
	var resumeDir string
	if structural != nil && structural.Matched > 0 {
		resumeDir, err = prepareMigrationDirectory(InspectDirectory(frozen))
		if err != nil {
			result.Error = err.Error()
			return result
		}
		defer func() { _ = os.RemoveAll(resumeDir) }()
		for _, file := range InspectDirectory(resumeDir).Files {
			if file.Filename == structuralFile {
				if err := os.WriteFile(filepath.Join(resumeDir, file.RelativePath), []byte(structural.RemainingSQL), 0600); err != nil {
					result.Error = err.Error()
					return result
				}
			}
		}
	}
	for _, action := range []string{"repair", "validate", "migrate"} {
		req.Action = action
		req.outputJSON = action == "validate"
		if action == "migrate" && structural != nil {
			req.migrationTarget = structuralVersion
			if resumeDir != "" {
				req.Directory = resumeDir
			}
		}
		step := run(ctx, req)
		if action == "validate" {
			report, err := parseReport(step)
			if err != nil || report.Valid == nil || !*report.Valid {
				step.Success, step.Error = false, "修复后校验失败，已停止重试迁移"
			}
		}
		result.Steps = append(result.Steps, step)
		if !step.Success {
			result.Error = step.Error
			return result
		}
	}
	if structural != nil {
		started := time.Now()
		verified, verifyErr := req.RecoveryPlanner(ctx, plannerSQLForScript(InspectDirectory(frozen), diagnosis.Issues[0].Script, diagnosis.Issues[0].SQL))
		step := ExecuteResult{Action: "verify-structure", Success: verifyErr == nil && verified != nil && verified.Safe && verified.Pending == 0 && verified.Matched == structural.Matched+structural.Pending}
		step.DurationMs = int(time.Since(started).Milliseconds())
		if step.Success {
			step.Output = fmt.Sprintf("已核对 %d 项结构操作，均与原始迁移脚本一致。", verified.Matched)
		} else {
			step.Error = "补齐后的结构核对未通过，已停止对齐原始脚本校验和"
			if verifyErr != nil {
				step.Output = verifyErr.Error()
			} else if verified != nil {
				step.Output = verified.Error
			}
		}
		result.Steps = append(result.Steps, step)
		if !step.Success {
			result.Error = step.Error
			return result
		}
	}
	if resumeDir != "" {
		req.Directory, req.migrationTarget, req.Action, req.outputJSON = frozen, "", "validate", true
		check := run(ctx, req)
		report, err := parseReport(check)
		if err != nil || report.Valid == nil {
			check.Success, check.Error = false, "结构补齐后无法核对原始脚本校验和，已停止"
		} else if !*report.Valid {
			check.Success = len(report.Invalid) == 1 && report.Invalid[0].Version == structuralVersion && report.Invalid[0].ErrorDetails.Code == "CHECKSUM_MISMATCH"
			if !check.Success {
				check.Error = "发现预期版本以外的迁移历史差异，已停止对齐校验和"
			} else {
				check.Error = ""
			}
		}
		check.Action = "verify-original-checksum"
		result.Steps = append(result.Steps, check)
		if !check.Success {
			result.Error = check.Error
			return result
		}
		if !*report.Valid {
			req.Action, req.outputJSON = "repair", false
			restored := run(ctx, req)
			restored.Action = "restore-checksum"
			result.Steps = append(result.Steps, restored)
			if !restored.Success {
				result.Error = restored.Error
				return result
			}
		}
		req.Action, req.outputJSON = "validate", true
		final := run(ctx, req)
		final.Action = "validate-final"
		finalReport, err := parseReport(final)
		if err != nil || finalReport.Valid == nil || !*finalReport.Valid {
			final.Success, final.Error = false, "结构已补齐，但原始脚本最终校验失败，请查看日志"
		}
		result.Steps = append(result.Steps, final)
		if !final.Success {
			result.Error = final.Error
			return result
		}
	}
	result.Success = true
	return result
}

// Repair rechecks a single-use diagnosis, repairs history, validates, then retries
// migration once. Supported schema plans preserve original checksums; existing data
// is never deleted, and any failed step stops the workflow.
func Repair(ctx context.Context, req ExecuteRequest, token string, confirmed bool) RepairResult {
	return repair(ctx, req, token, confirmed, Execute)
}
