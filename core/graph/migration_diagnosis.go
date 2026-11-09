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

package graph

import (
	"context"
	"errors"
	"strings"

	"github.com/clidey/whodb/core/graph/model"
	"github.com/clidey/whodb/core/src"
	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/flyway"
	"github.com/clidey/whodb/core/src/source/adapters"
	"github.com/clidey/whodb/core/src/sourcecatalog"
)

func migrationRequest(ctx context.Context, input model.SourceMigrationInput) (flyway.ExecuteRequest, error) {
	if input.Target == nil {
		return flyway.ExecuteRequest{}, errors.New("target database connection is required")
	}
	credentials, err := resolveTargetCredentials(ctx, *input.Target)
	if err != nil {
		return flyway.ExecuteRequest{}, err
	}
	req := flyway.ExecuteRequest{Credentials: credentials, Directory: input.Directory, BaselineOnMigrate: true, ValidateOnMigrate: true}
	if input.Target.Database != nil {
		req.TargetDatabase = strings.TrimSpace(*input.Target.Database)
	}
	if input.TargetSchema != nil {
		req.TargetSchema = strings.TrimSpace(*input.TargetSchema)
	}
	if input.BaselineOnMigrate != nil {
		req.BaselineOnMigrate = *input.BaselineOnMigrate
	}
	if input.ValidateOnMigrate != nil {
		req.ValidateOnMigrate = *input.ValidateOnMigrate
	}
	if input.OutOfOrder != nil {
		req.OutOfOrder = *input.OutOfOrder
	}
	if spec, ok := sourcecatalog.Find(credentials.SourceType); ok {
		if plugin := src.MainEngine.Choose(engine.DatabaseType(spec.Connector)); plugin != nil {
			pluginCredentials := adapters.EngineCredentials(spec, credentials)
			if req.TargetDatabase != "" {
				pluginCredentials.Database = req.TargetDatabase
			}
			req.RecoveryPlanner = func(ctx context.Context, sql string) (*engine.MigrationRecoveryPlan, error) {
				return plugin.PlanMigrationRecovery(&engine.PluginConfig{Credentials: pluginCredentials, Context: ctx}, req.TargetSchema, sql)
			}
		}
	}
	return req, nil
}

func diagnoseSourceMigrations(ctx context.Context, input model.SourceMigrationInput) (*model.SourceMigrationDiagnosis, error) {
	req, err := migrationRequest(ctx, input)
	if err != nil {
		return nil, err
	}
	diagnosis := flyway.Diagnose(ctx, req)
	result := &model.SourceMigrationDiagnosis{Healthy: diagnosis.Healthy, RepairAllowed: diagnosis.RepairAllowed, Issues: []*model.SourceMigrationIssue{}}
	if diagnosis.Token != "" {
		result.Token = &diagnosis.Token
	}
	if diagnosis.Error != "" {
		result.Error = &diagnosis.Error
	}
	for _, issue := range diagnosis.Issues {
		item := &model.SourceMigrationIssue{Version: issue.Version, Script: issue.Script, Code: issue.Code, Message: issue.Message, SQL: issue.SQL}
		if plan := issue.Recovery; plan != nil {
			item.Recovery = &model.SourceMigrationRecoveryPlan{Safe: plan.Safe, Matched: plan.Matched, Pending: plan.Pending, Steps: []*model.SourceMigrationRecoveryStep{}}
			if plan.Error != "" {
				item.Recovery.Error = &plan.Error
			}
			for _, step := range plan.Steps {
				item.Recovery.Steps = append(item.Recovery.Steps, &model.SourceMigrationRecoveryStep{DuplicateDataAvailable: step.DuplicateQuery != "", Kind: model.SourceMigrationRecoveryKind(step.Kind), Object: step.Object, Status: model.SourceMigrationRecoveryStatus(step.Status), Reason: step.Reason, SQL: step.SQL})
			}
		}
		result.Issues = append(result.Issues, item)
	}
	return result, nil
}

func repairSourceMigrations(ctx context.Context, input model.SourceMigrationRepairInput) (*model.SourceMigrationRepairResult, error) {
	if input.Migration == nil {
		return nil, errors.New("migration diagnosis input is required")
	}
	req, err := migrationRequest(ctx, *input.Migration)
	if err != nil {
		return nil, err
	}
	req.StructureRecoveryConfirmed = input.ConfirmPlanExecution
	repaired := flyway.Repair(ctx, req, input.Token, input.ConfirmPartialChangesResolved)
	result := &model.SourceMigrationRepairResult{Success: repaired.Success, Steps: []*model.FlywayExecutionResult{}}
	if repaired.Error != "" {
		result.Error = &repaired.Error
	}
	for _, step := range repaired.Steps {
		item := &model.FlywayExecutionResult{Success: step.Success, Action: step.Action, Command: step.Command, Output: step.Output, DurationMs: step.DurationMs}
		if step.Error != "" {
			errText := step.Error
			item.Error = &errText
		}
		result.Steps = append(result.Steps, item)
	}
	return result, nil
}

func diagnosedDuplicateStep(ctx context.Context, req flyway.ExecuteRequest, version, object string) (engine.MigrationRecoveryStep, error) {
	diagnosis := flyway.Diagnose(ctx, req)
	if diagnosis.Error != "" {
		return engine.MigrationRecoveryStep{}, errors.New(diagnosis.Error)
	}
	for _, issue := range diagnosis.Issues {
		if issue.Version != version || issue.Recovery == nil {
			continue
		}
		for _, step := range issue.Recovery.Steps {
			if step.Object == object && step.Status == "CONFLICT" && step.DuplicateQuery != "" {
				return step, nil
			}
		}
	}
	return engine.MigrationRecoveryStep{}, errors.New("该冲突不存在或已消除，请重新诊断")
}

func migrationDuplicatePlugin(req flyway.ExecuteRequest) (engine.PluginFunctions, *engine.PluginConfig, error) {
	spec, ok := sourcecatalog.Find(req.Credentials.SourceType)
	if !ok {
		return nil, nil, errors.New("数据源不支持重复数据处理")
	}
	plugin := src.MainEngine.Choose(engine.DatabaseType(spec.Connector))
	if plugin == nil {
		return nil, nil, errors.New("数据源不支持重复数据处理")
	}
	credentials := adapters.EngineCredentials(spec, req.Credentials)
	if req.TargetDatabase != "" {
		credentials.Database = req.TargetDatabase
	}
	return plugin, &engine.PluginConfig{Credentials: credentials}, nil
}

func sourceMigrationDuplicateRows(ctx context.Context, input model.SourceMigrationInput, version, object string) (*model.RowsResult, error) {
	req, err := migrationRequest(ctx, input)
	if err != nil {
		return nil, err
	}
	step, err := diagnosedDuplicateStep(ctx, req, version, object)
	if err != nil {
		return nil, err
	}
	plugin, config, err := migrationDuplicatePlugin(req)
	if err != nil {
		return nil, err
	}
	config.Context = ctx
	result, err := plugin.RawExecute(config, step.DuplicateQuery)
	if err != nil {
		return nil, err
	}
	return rowsResultToModel(result), nil
}

func sourceMigrationDuplicateCleanup(ctx context.Context, input model.SourceMigrationInput, version, object string, strategy model.SourceDuplicateRetentionStrategy, fingerprint *string) (*model.SourceMigrationDuplicateCleanup, error) {
	if strategy != model.SourceDuplicateRetentionStrategyFirst && strategy != model.SourceDuplicateRetentionStrategyLast {
		return nil, errors.New("请选择保留第一条或最后一条")
	}
	req, err := migrationRequest(ctx, input)
	if err != nil {
		return nil, err
	}
	step, err := diagnosedDuplicateStep(ctx, req, version, object)
	if err != nil {
		return nil, err
	}
	plugin, config, err := migrationDuplicatePlugin(req)
	if err != nil {
		return nil, err
	}
	config.Context = ctx
	result, err := plugin.CleanMigrationDuplicates(config, step, strategy == model.SourceDuplicateRetentionStrategyLast, fingerprint)
	if err != nil {
		return nil, err
	}
	return &model.SourceMigrationDuplicateCleanup{Groups: result.Groups, RowsToDelete: result.RowsToDelete, Deleted: result.Deleted, PrimaryKeyColumns: result.PrimaryKeyColumns, Fingerprint: result.Fingerprint}, nil
}
