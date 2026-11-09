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

package gorm_plugin

import (
	"errors"

	"github.com/clidey/whodb/core/src/engine"
)

// PlanMigrationRecovery reports unsupported unless the database plugin supplies a verifier.
func (p *GormPlugin) PlanMigrationRecovery(_ *engine.PluginConfig, _ string, _ string) (*engine.MigrationRecoveryPlan, error) {
	return nil, errors.ErrUnsupported
}

// CleanMigrationDuplicates reports unsupported unless the connector implements transactional cleanup.
func (p *GormPlugin) CleanMigrationDuplicates(_ *engine.PluginConfig, _ engine.MigrationRecoveryStep, _ bool, _ *string) (*engine.MigrationDuplicateCleanup, error) {
	return nil, errors.ErrUnsupported
}
