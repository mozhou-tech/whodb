package openobserve

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/query"
	"github.com/clidey/whodb/core/src/sourcecatalog/specs"
	"github.com/clidey/whodb/core/src/sqlident"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func buildQuery(req *engine.GetRowsRequest, columns []engine.Column, count bool) (string, error) {
	name, err := sqlident.New("", req.StorageUnit)
	if err != nil {
		return "", err
	}
	table, err := name.SQL(sqlident.DoubleQuote)
	if err != nil {
		return "", err
	}
	// The search API accepts SQL text only. GORM builds bound expressions and its
	// PostgreSQL dialect renders escaped literals without opening a connection.
	db, err := gorm.Open(postgres.New(postgres.Config{}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		return "", err
	}
	statement := db.Table(table)
	types := make(map[string]string, len(columns))
	for _, column := range columns {
		types[column.Name] = column.Type
	}
	filter, err := buildFilter(req.Where, types)
	if err != nil {
		return "", err
	}
	if filter != nil {
		statement = statement.Where(filter)
	}
	if count {
		statement = statement.Select(`count(*) AS "count"`)
	} else {
		for _, condition := range req.Sort {
			column, err := sqlident.New("", condition.Column)
			if err != nil {
				return "", err
			}
			quoted, err := column.SQL(sqlident.DoubleQuote)
			if err != nil {
				return "", err
			}
			if condition.Direction != query.SortDirectionAsc && condition.Direction != query.SortDirectionDesc {
				return "", fmt.Errorf("invalid sort direction")
			}
			statement = statement.Order(quoted + " " + string(condition.Direction))
		}
		if len(req.Sort) == 0 {
			statement = statement.Order(`"_timestamp" DESC`)
		}
	}
	var rows []map[string]any
	return statement.ToSQL(func(tx *gorm.DB) *gorm.DB { return tx.Find(&rows) }), nil
}

func buildFilter(condition *query.WhereCondition, types map[string]string) (clause.Expression, error) {
	if condition == nil {
		return nil, nil
	}
	switch condition.Type {
	case query.WhereConditionTypeAtomic:
		atomic := condition.Atomic
		if atomic == nil {
			return nil, fmt.Errorf("missing OpenObserve filter predicate")
		}
		if !slices.Contains(specs.OpenObserveOperators, atomic.Operator) {
			return nil, fmt.Errorf("unsupported OpenObserve operator: %s", atomic.Operator)
		}
		name, err := sqlident.New("", atomic.Key)
		if err != nil {
			return nil, err
		}
		column, err := name.SQL(sqlident.DoubleQuote)
		if err != nil {
			return nil, err
		}
		expression := column + " " + atomic.Operator
		if atomic.Operator == "IS NULL" || atomic.Operator == "IS NOT NULL" {
			return clause.Expr{SQL: expression}, nil
		}
		var value any = atomic.Value
		typ := types[atomic.Key]
		switch {
		case strings.HasPrefix(typ, "UInt"):
			value, err = strconv.ParseUint(atomic.Value, 10, 64)
		case strings.HasPrefix(typ, "Int"):
			value, err = strconv.ParseInt(atomic.Value, 10, 64)
		case strings.HasPrefix(typ, "Float"):
			value, err = strconv.ParseFloat(atomic.Value, 64)
		case typ == "Boolean":
			value, err = strconv.ParseBool(atomic.Value)
		}
		if err != nil {
			return nil, fmt.Errorf("invalid filter value for %s: %w", typ, err)
		}
		return clause.Expr{SQL: expression + " ?", Vars: []any{value}}, nil
	case query.WhereConditionTypeAnd, query.WhereConditionTypeOr:
		group := condition.And
		if condition.Type == query.WhereConditionTypeOr {
			group = condition.Or
		}
		if group == nil {
			return nil, fmt.Errorf("missing OpenObserve filter group")
		}
		expressions := make([]clause.Expression, 0, len(group.Children))
		for _, child := range group.Children {
			expression, err := buildFilter(child, types)
			if err != nil {
				return nil, err
			}
			if expression != nil {
				expressions = append(expressions, expression)
			}
		}
		if condition.Type == query.WhereConditionTypeOr {
			return clause.Or(expressions...), nil
		}
		return clause.And(expressions...), nil
	default:
		return nil, fmt.Errorf("unsupported OpenObserve filter type")
	}
}
