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

package engine

// MigrationRecoveryStep describes a verified additive schema operation.
type MigrationRecoveryStep struct {
	Kind   string
	Object string
	Status string
	Reason string
	SQL    string
}

// MigrationRecoveryPlan contains a read-only comparison with the current schema.
// RemainingSQL may only contain additive statements whose effects are missing.
type MigrationRecoveryPlan struct {
	Safe         bool
	Steps        []MigrationRecoveryStep
	RemainingSQL string
	Fingerprint  string
	Matched      int
	Pending      int
	Error        string
}
