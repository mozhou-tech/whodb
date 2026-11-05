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
	"strings"

	"github.com/clidey/whodb/core/graph/model"
	"github.com/clidey/whodb/core/src/flyway"
)

func flywayStatusToModel(status flyway.Status) *model.FlywayStatus {
	var verPtr *string
	if strings.TrimSpace(status.Version) != "" {
		v := status.Version
		verPtr = &v
	}
	var pathPtr *string
	if strings.TrimSpace(status.BinaryPath) != "" {
		p := status.BinaryPath
		pathPtr = &p
	}
	var methodPtr *string
	if strings.TrimSpace(status.InstallMethod) != "" {
		m := status.InstallMethod
		methodPtr = &m
	}
	return &model.FlywayStatus{
		Installed:     status.Installed,
		Version:       verPtr,
		BinaryPath:    pathPtr,
		InstallMethod: methodPtr,
	}
}

func getFlywayStatus(ctx context.Context) (*model.FlywayStatus, error) {
	return flywayStatusToModel(flyway.GetStatus(ctx)), nil
}

func installFlyway(ctx context.Context) (*model.FlywayInstallResult, error) {
	res := flyway.Install(ctx)
	var errPtr *string
	if strings.TrimSpace(res.Error) != "" {
		e := res.Error
		errPtr = &e
	}
	return &model.FlywayInstallResult{
		Success: res.Success,
		Status:  flywayStatusToModel(res.Status),
		Output:  res.Output,
		Error:   errPtr,
	}, nil
}

func inspectFlywayDirectory(_ context.Context, directory string) (*model.FlywayDirectoryPreview, error) {
	preview := flyway.InspectDirectory(directory)
	files := make([]*model.FlywayMigrationFile, 0, len(preview.Files))
	for _, f := range preview.Files {
		files = append(files, &model.FlywayMigrationFile{
			Filename:     f.Filename,
			RelativePath: f.RelativePath,
			Version:      f.Version,
			Description:  f.Description,
			Type:         f.Type,
			SizeBytes:    int(f.SizeBytes),
		})
	}
	var errPtr *string
	if strings.TrimSpace(preview.Error) != "" {
		e := preview.Error
		errPtr = &e
	}
	return &model.FlywayDirectoryPreview{
		Directory: preview.Directory,
		Exists:    preview.Exists,
		Files:     files,
		Error:     errPtr,
	}, nil
}

func executeFlyway(ctx context.Context, input model.ExecuteFlywayInput) (*model.FlywayExecutionResult, error) {
	if input.Target == nil {
		errMsg := "target database connection is required"
		return &model.FlywayExecutionResult{
			Success: false,
			Action:  input.Action,
			Error:   &errMsg,
		}, nil
	}

	creds, err := resolveTargetCredentials(ctx, *input.Target)
	if err != nil {
		errMsg := err.Error()
		return &model.FlywayExecutionResult{
			Success: false,
			Action:  input.Action,
			Error:   &errMsg,
		}, nil
	}

	targetDB := ""
	if input.Target.Database != nil {
		targetDB = strings.TrimSpace(*input.Target.Database)
	}
	targetSchema := ""
	if input.TargetSchema != nil {
		targetSchema = strings.TrimSpace(*input.TargetSchema)
	}
	baselineOnMigrate := true
	if input.BaselineOnMigrate != nil {
		baselineOnMigrate = *input.BaselineOnMigrate
	}
	outOfOrder := false
	if input.OutOfOrder != nil {
		outOfOrder = *input.OutOfOrder
	}
	validateOnMigrate := false
	if input.ValidateOnMigrate != nil {
		validateOnMigrate = *input.ValidateOnMigrate
	}

	res := flyway.Execute(ctx, flyway.ExecuteRequest{
		Credentials:       creds,
		TargetDatabase:    targetDB,
		TargetSchema:      targetSchema,
		Directory:         input.Directory,
		Action:            input.Action,
		BaselineOnMigrate: baselineOnMigrate,
		OutOfOrder:        outOfOrder,
		ValidateOnMigrate: validateOnMigrate,
	})

	var errPtr *string
	if strings.TrimSpace(res.Error) != "" {
		e := res.Error
		errPtr = &e
	}
	return &model.FlywayExecutionResult{
		Success:    res.Success,
		Action:     res.Action,
		Command:    res.Command,
		Output:     res.Output,
		Error:      errPtr,
		DurationMs: res.DurationMs,
	}, nil
}
