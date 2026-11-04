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
	"fmt"
	"maps"
	"strings"

	"github.com/clidey/whodb/core/graph/model"
	"github.com/clidey/whodb/core/src"
	"github.com/clidey/whodb/core/src/auth"
	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/env"
	"github.com/clidey/whodb/core/src/source"
	"github.com/clidey/whodb/core/src/source/adapters"
	"github.com/clidey/whodb/core/src/sourcecatalog"
)

func resolveTargetCredentials(ctx context.Context, target model.SourceTargetConnectionInput) (*source.Credentials, error) {
	inputValues := recordInputsToMap(target.Values)
	profileID := ""
	if target.ProfileID != nil {
		profileID = strings.TrimSpace(*target.ProfileID)
	}

	var creds *source.Credentials
	if profileID != "" {
		if _, foundCreds, ok := src.FindSourceProfile(profileID); ok && foundCreds != nil {
			mergedValues, err := auth.MergeSourceProfileValues(foundCreds.SourceType, foundCreds.Values, inputValues)
			if err != nil {
				return nil, err
			}
			foundCreds.Values = mergedValues
			creds = foundCreds
		} else if current := auth.GetSourceCredentials(ctx); current != nil && current.ID != nil && strings.TrimSpace(*current.ID) == profileID {
			clonedValues := current.CloneValues()
			maps.Copy(clonedValues, inputValues)
			idCopy := *current.ID
			var tokenCopy *string
			if current.AccessToken != nil {
				v := *current.AccessToken
				tokenCopy = &v
			}
			creds = &source.Credentials{
				ID:          &idCopy,
				SourceType:  current.SourceType,
				Values:      clonedValues,
				AccessToken: tokenCopy,
				IsProfile:   current.IsProfile,
			}
		} else if stored, err := auth.LoadCredentials(profileID); err == nil && stored != nil {
			clonedValues := stored.CloneValues()
			maps.Copy(clonedValues, inputValues)
			stored.Values = clonedValues
			creds = stored
		}
	}

	if creds == nil {
		sourceType := ""
		if target.SourceType != nil {
			sourceType = strings.TrimSpace(*target.SourceType)
		}
		if sourceType == "" {
			if profileID != "" {
				return nil, errors.New("target profile does not exist or is not authorized")
			}
			return nil, errors.New("target connection requires either ProfileId or SourceType")
		}
		if env.DisableCredentialForm && profileID == "" {
			return nil, errors.New("credential form login is disabled; please select a target profile")
		}
		var idPtr *string
		if profileID != "" {
			idCopy := profileID
			idPtr = &idCopy
		}
		creds = &source.Credentials{
			ID:          idPtr,
			SourceType:  sourceType,
			Values:      inputValues,
			AccessToken: target.AccessToken,
		}
		if current := auth.GetSourceCredentials(ctx); current != nil && current.SourceType == creds.SourceType && len(creds.Values) == 0 {
			creds.Values = mergeCurrentSourceValues(current, creds.Values)
		}
	}

	if target.Database != nil {
		if dbName := strings.TrimSpace(*target.Database); dbName != "" {
			if creds.Values == nil {
				creds.Values = map[string]string{}
			}
			creds.Values["Database"] = dbName
		}
	}

	return creds, nil
}

func resolveTargetDatabaseSession(ctx context.Context, target model.SourceTargetConnectionInput) (source.TypeSpec, *adapters.DatabaseSession, error) {
	creds, err := resolveTargetCredentials(ctx, target)
	if err != nil {
		return source.TypeSpec{}, nil, err
	}

	spec, ok := sourcecatalog.Find(creds.SourceType)
	if !ok {
		return source.TypeSpec{}, nil, fmt.Errorf("unsupported target source type: %s", creds.SourceType)
	}

	session, err := source.Open(ctx, spec, creds)
	if err != nil {
		return source.TypeSpec{}, nil, err
	}

	dbSession, ok := session.(*adapters.DatabaseSession)
	if !ok {
		return source.TypeSpec{}, nil, fmt.Errorf("target source %s does not support database copy operations", spec.ID)
	}

	return spec, dbSession, nil
}

func sourceTargetDatabases(ctx context.Context, target model.SourceTargetConnectionInput) ([]string, error) {
	_, dbSession, err := resolveTargetDatabaseSession(ctx, target)
	if err != nil {
		return nil, err
	}
	return dbSession.ListTargetDatabases(ctx)
}

func sourceTargetSchemas(ctx context.Context, target model.SourceTargetConnectionInput) ([]string, error) {
	_, dbSession, err := resolveTargetDatabaseSession(ctx, target)
	if err != nil {
		return nil, err
	}
	return dbSession.ListTargetSchemas(ctx)
}

func copySourceObjects(ctx context.Context, input model.CopySourceObjectsInput) (*model.CopySourceObjectsResult, error) {
	if len(input.ObjectNames) == 0 {
		return nil, errors.New("at least one object must be selected for copying")
	}
	if input.Target == nil {
		return nil, errors.New("target connection is required")
	}

	sourceSpec, rawSourceSession, err := getSourceSessionForContext(ctx)
	if err != nil {
		return nil, err
	}
	sourceDBSession, ok := rawSourceSession.(*adapters.DatabaseSession)
	if !ok {
		return nil, fmt.Errorf("source %s does not support database copy operations", sourceSpec.ID)
	}

	targetSpec, targetDBSession, err := resolveTargetDatabaseSession(ctx, *input.Target)
	if err != nil {
		return nil, err
	}
	if sourceSpec.Connector != targetSpec.Connector {
		return nil, fmt.Errorf("cannot copy between different database types (%s -> %s)", sourceSpec.Label, targetSpec.Label)
	}

	includeData := input.Mode == model.CopyDataModeStructureAndData
	var existingStrategy engine.CopyExistingStrategy
	switch input.ExistingStrategy {
	case model.CopyExistingStrategySkip:
		existingStrategy = engine.CopyExistingStrategySkip
	case model.CopyExistingStrategyOverwrite:
		existingStrategy = engine.CopyExistingStrategyOverwrite
	case model.CopyExistingStrategyAppend:
		existingStrategy = engine.CopyExistingStrategyAppend
	default:
		existingStrategy = engine.CopyExistingStrategySkip
	}

	targetSchema := ""
	if input.TargetSchema != nil {
		targetSchema = strings.TrimSpace(*input.TargetSchema)
	}

	sourceParent := sourceRefFromInput(input.SourceParent)
	details, err := sourceDBSession.CopyObjects(
		ctx,
		sourceParent,
		targetDBSession,
		targetSchema,
		input.ObjectNames,
		includeData,
		existingStrategy,
	)
	if err != nil {
		return nil, err
	}

	gqlDetails := make([]*model.CopyTableDetail, 0, len(details))
	succeededCount := 0
	skippedCount := 0
	failedCount := 0
	totalRowsCopied := 0

	for _, d := range details {
		var status model.CopyTableStatus
		switch d.Status {
		case engine.CopyTableStatusSuccess:
			status = model.CopyTableStatusSuccess
			succeededCount++
		case engine.CopyTableStatusSkipped:
			status = model.CopyTableStatusSkipped
			skippedCount++
		default:
			status = model.CopyTableStatusFailed
			failedCount++
		}
		totalRowsCopied += d.RowsCopied

		var msgPtr *string
		if d.Message != "" {
			msg := d.Message
			msgPtr = &msg
		}
		gqlDetails = append(gqlDetails, &model.CopyTableDetail{
			Name:       d.Name,
			Status:     status,
			RowsCopied: d.RowsCopied,
			Message:    msgPtr,
		})
	}

	return &model.CopySourceObjectsResult{
		Status:          failedCount == 0,
		TotalTables:     len(gqlDetails),
		SucceededCount:  succeededCount,
		SkippedCount:    skippedCount,
		FailedCount:     failedCount,
		TotalRowsCopied: totalRowsCopied,
		Details:         gqlDetails,
	}, nil
}
