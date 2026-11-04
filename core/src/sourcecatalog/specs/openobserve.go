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
