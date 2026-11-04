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

package specs

import "github.com/clidey/whodb/core/src/source"

// OpenObserveOperators are the predicates supported by the log stream browser.
var OpenObserveOperators = []string{"=", "!=", ">", ">=", "<", "<=", "LIKE", "NOT LIKE", "IS NULL", "IS NOT NULL"}

// OpenObserveTypeDefinitions describes the Arrow types exposed by stream schemas.
var OpenObserveTypeDefinitions = []source.TypeDefinition{
	{ID: "Utf8", Label: "Utf8", Category: source.TypeCategoryText},
	{ID: "Int64", Label: "Int64", Category: source.TypeCategoryNumeric},
	{ID: "UInt64", Label: "UInt64", Category: source.TypeCategoryNumeric},
	{ID: "Float64", Label: "Float64", Category: source.TypeCategoryNumeric},
	{ID: "Boolean", Label: "Boolean", Category: source.TypeCategoryBoolean},
}
