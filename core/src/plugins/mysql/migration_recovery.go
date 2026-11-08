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

package mysql

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/pingcap/tidb/pkg/parser/ast"
	"gorm.io/gorm"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/plugins"
	"github.com/clidey/whodb/core/src/sqlident"
)

type migrationTableState struct {
	Definition      migrationTable
	Exists          bool
	OriginalColumns map[string]bool
}

type mysqlMigrationPlanner struct {
	db            *gorm.DB
	database      string
	defaults      migrationDefaults
	tables        map[string]*migrationTableState
	observed      map[string]json.RawMessage
	remaining     []string
	futureColumns map[string]map[string]bool
	futureIndexes map[string]map[string]bool
	plan          *engine.MigrationRecoveryPlan
}

// PlanMigrationRecovery verifies additive MySQL DDL against live metadata without writes.
// MariaDB and compatible wrapper plugins inherit the same conservative verifier.
func (p *MySQLPlugin) PlanMigrationRecovery(config *engine.PluginConfig, schema string, sql string) (*engine.MigrationRecoveryPlan, error) {
	if schema != "" && schema != config.Credentials.Database {
		return &engine.MigrationRecoveryPlan{Error: "目标架构与数据库不一致，不能自动核对", Steps: []engine.MigrationRecoveryStep{}}, nil
	}
	nodes, err := parseMigrationSQL(sql)
	if err != nil {
		return &engine.MigrationRecoveryPlan{Error: err.Error(), Steps: []engine.MigrationRecoveryStep{}}, nil
	}
	return plugins.WithConnection(config, p.DB, func(db *gorm.DB) (*engine.MigrationRecoveryPlan, error) {
		planner := &mysqlMigrationPlanner{db: db, database: config.Credentials.Database, tables: map[string]*migrationTableState{}, observed: map[string]json.RawMessage{}, plan: &engine.MigrationRecoveryPlan{Safe: true, Steps: []engine.MigrationRecoveryStep{}}}
		var defaults struct {
			Charset   string `gorm:"column:DEFAULT_CHARACTER_SET_NAME"`
			Collation string `gorm:"column:DEFAULT_COLLATION_NAME"`
		}
		if err := db.Raw("SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", planner.database).Scan(&defaults).Error; err != nil {
			return nil, err
		}
		var storageEngine string
		if err := db.Raw("SELECT @@default_storage_engine").Scan(&storageEngine).Error; err != nil {
			return nil, err
		}
		var sqlMode string
		if err := db.Raw("SELECT @@sql_mode").Scan(&sqlMode).Error; err != nil {
			return nil, err
		}
		if strings.Contains(sqlMode, "NO_BACKSLASH_ESCAPES") || strings.Contains(sqlMode, "ANSI_QUOTES") {
			return &engine.MigrationRecoveryPlan{Error: "当前数据库使用特殊引号或反斜杠 SQL 模式，需要人工核对脚本", Steps: []engine.MigrationRecoveryStep{}}, nil
		}
		planner.defaults = migrationDefaults{Engine: strings.ToLower(storageEngine), Charset: strings.ToLower(defaults.Charset), Collation: strings.ToLower(defaults.Collation)}
		if err := db.Raw("SELECT DEFAULT_COLLATE_NAME FROM information_schema.CHARACTER_SETS WHERE CHARACTER_SET_NAME = ?", defaults.Charset).Scan(&planner.defaults.CharsetCollation).Error; err != nil {
			return nil, err
		}
		if defaults.Charset == "utf8mb4" {
			var variables []struct {
				Name  string `gorm:"column:Variable_name"`
				Value string `gorm:"column:Value"`
			}
			if err := db.Raw("SHOW VARIABLES LIKE 'default_collation_for_utf8mb4'").Scan(&variables).Error; err != nil {
				return nil, err
			}
			if len(variables) == 1 {
				planner.defaults.CharsetCollation = variables[0].Value
			}
		}
		planner.futureColumns, planner.futureIndexes = map[string]map[string]bool{}, map[string]map[string]bool{}
		for _, node := range nodes {
			switch statement := node.(type) {
			case *ast.AlterTableStmt:
				if planner.futureColumns[statement.Table.Name.O] == nil {
					planner.futureColumns[statement.Table.Name.O] = map[string]bool{}
				}
				for _, spec := range statement.Specs {
					if spec.Tp == ast.AlterTableAddColumns {
						for _, column := range spec.NewColumns {
							planner.futureColumns[statement.Table.Name.O][strings.ToLower(column.Name.Name.O)] = true
						}
					}
				}
			case *ast.CreateIndexStmt:
				if planner.futureIndexes[statement.Table.Name.O] == nil {
					planner.futureIndexes[statement.Table.Name.O] = map[string]bool{}
				}
				planner.futureIndexes[statement.Table.Name.O][strings.ToLower(statement.IndexName)] = true
			}
		}
		for _, node := range nodes {
			if err := planner.inspect(node); err != nil {
				planner.plan.Safe, planner.plan.Error = false, err.Error()
				return planner.plan, nil
			}
		}
		encoded, err := json.Marshal(planner.observed)
		if err != nil {
			return nil, err
		}
		fingerprint := sha256.Sum256(encoded)
		planner.plan.Fingerprint = hex.EncodeToString(fingerprint[:])
		planner.plan.RemainingSQL = strings.Join(planner.remaining, ";\n")
		if len(planner.remaining) == 0 {
			planner.plan.RemainingSQL = "-- 已核对全部结构操作，无需重复执行。\n"
		} else {
			planner.plan.RemainingSQL += ";\n"
		}
		return planner.plan, nil
	})
}

func (p *mysqlMigrationPlanner) table(name *ast.TableName) (*migrationTableState, error) {
	if name.Schema.O != "" && name.Schema.O != p.database {
		return nil, fmt.Errorf("迁移包含其他数据库 %s，不能自动处理", name.Schema.O)
	}
	if table, ok := p.tables[name.Name.O]; ok {
		return table, nil
	}
	state := &migrationTableState{OriginalColumns: map[string]bool{}}
	var count int64
	if err := p.db.Raw("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE = 'BASE TABLE'", p.database, name.Name.O).Scan(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		identifier, err := sqlident.New(p.database, name.Name.O)
		if err != nil {
			return nil, err
		}
		quoted, err := identifier.SQL(sqlident.Backtick)
		if err != nil {
			return nil, err
		}
		rows, err := p.db.Raw("SHOW CREATE TABLE " + quoted).Rows()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			return nil, fmt.Errorf("无法读取表 %s 的结构", name.Name.O)
		}
		var tableName, ddl string
		if err := rows.Scan(&tableName, &ddl); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		nodes, err := parseMigrationSQL(ddl)
		if err != nil {
			return nil, err
		}
		create, ok := nodes[0].(*ast.CreateTableStmt)
		if !ok || len(nodes) != 1 {
			return nil, fmt.Errorf("无法核对表 %s 的建表定义", name.Name.O)
		}
		state.Definition, err = migrationTableDefinition(create, p.defaults)
		if err != nil {
			return nil, err
		}
		state.Exists = true
		for column := range state.Definition.Columns {
			state.OriginalColumns[column] = true
		}
	}
	p.tables[name.Name.O] = state
	observed, err := json.Marshal(state.Definition)
	if err != nil {
		return nil, err
	}
	p.observed[name.Name.O] = observed
	return state, nil
}

func (p *mysqlMigrationPlanner) step(kind, object, sql string, matched bool) {
	for _, existing := range p.plan.Steps {
		if existing.Kind == kind && existing.Object == object {
			p.plan.Safe, p.plan.Error = false, "同一脚本重复定义对象 "+object+"，需要人工核对"
			return
		}
	}
	status, reason := "PENDING", "对象尚不存在，将执行该新增操作"
	if matched {
		status, reason = "MATCHED", "已有结构与脚本定义一致，将跳过重复操作"
		p.plan.Matched++
	} else {
		p.plan.Pending++
		p.remaining = append(p.remaining, sql)
	}
	p.plan.Steps = append(p.plan.Steps, engine.MigrationRecoveryStep{Kind: kind, Object: object, SQL: sql, Status: status, Reason: reason})
}

func (p *mysqlMigrationPlanner) conflict(kind, object, sql, reason string) error {
	p.plan.Steps = append(p.plan.Steps, engine.MigrationRecoveryStep{Kind: kind, Object: object, SQL: sql, Status: "CONFLICT", Reason: reason})
	return fmt.Errorf("%s：%s", object, reason)
}

func (p *mysqlMigrationPlanner) createConflict(table string, actual, expected migrationTable) string {
	// Objects added later in this same script are checked by their own steps.
	// Unrelated extra columns or indexes still prevent automatic reconciliation.
	projected := actual
	projected.Columns, projected.Indexes = maps.Clone(actual.Columns), maps.Clone(actual.Indexes)
	if len(actual.Order) < len(expected.Order) {
		return "已有表缺少脚本要求的字段"
	}
	projected.Order = actual.Order[:len(expected.Order)]
	for name := range actual.Columns {
		if _, exists := expected.Columns[name]; !exists {
			if !p.futureColumns[table][name] {
				return "已有表存在脚本未声明的字段 " + name
			}
			delete(projected.Columns, name)
		}
	}
	for name := range actual.Indexes {
		if _, exists := expected.Indexes[name]; !exists {
			if !p.futureIndexes[table][name] {
				return "已有表存在脚本未声明的索引 " + name
			}
			delete(projected.Indexes, name)
		}
	}
	if reflect.DeepEqual(projected, expected) {
		return ""
	}
	for _, name := range expected.Order {
		if !reflect.DeepEqual(projected.Columns[name], expected.Columns[name]) {
			return "字段 " + name + " 的类型、可空性、默认值或属性与脚本不同"
		}
	}
	if !reflect.DeepEqual(projected.Indexes, expected.Indexes) {
		return "已有表的索引字段、顺序或唯一性与脚本不同"
	}
	if !reflect.DeepEqual(projected.Order, expected.Order) {
		return "已有表的字段顺序与脚本不同"
	}
	if projected.Engine != expected.Engine {
		return "已有表的存储引擎与脚本不同"
	}
	if projected.Charset != expected.Charset || projected.Collation != expected.Collation {
		return "已有表的字符集或排序规则与脚本不同"
	}
	return "已有表的备注或其他属性与脚本不同"
}

func (p *mysqlMigrationPlanner) inspect(node ast.StmtNode) error {
	sql, err := restoreMigrationNode(node)
	if err != nil {
		return err
	}
	switch statement := node.(type) {
	case *ast.CreateTableStmt:
		state, err := p.table(statement.Table)
		if err != nil {
			return err
		}
		expected, err := migrationTableDefinition(statement, p.defaults)
		if err != nil {
			return err
		}
		if state.Exists {
			if reason := p.createConflict(statement.Table.Name.O, state.Definition, expected); reason != "" {
				return p.conflict("CREATE_TABLE", statement.Table.Name.O, sql, reason)
			}
		}
		p.step("CREATE_TABLE", statement.Table.Name.O, sql, state.Exists)
		if !state.Exists {
			state.Definition = expected
		}
	case *ast.AlterTableStmt:
		if len(statement.Specs) != 1 || statement.Specs[0].Tp != ast.AlterTableAddColumns {
			return fmt.Errorf("自动恢复只支持单字段 ADD COLUMN，不处理删除或修改字段")
		}
		spec := statement.Specs[0]
		if len(spec.NewColumns) != 1 || len(spec.NewConstraints) != 0 || (spec.Position != nil && spec.Position.Tp != ast.ColumnPositionNone) {
			return fmt.Errorf("带字段位置或附加约束的 ALTER TABLE 需要人工处理")
		}
		column := spec.NewColumns[0]
		for _, option := range column.Options {
			if option.Tp == ast.ColumnOptionPrimaryKey || option.Tp == ast.ColumnOptionUniqKey || option.Tp == ast.ColumnOptionAutoIncrement {
				return fmt.Errorf("带主键、唯一或自增约束的新增字段需要人工处理")
			}
		}
		state, err := p.table(statement.Table)
		if err != nil {
			return err
		}
		if state.Definition.Columns == nil {
			return fmt.Errorf("表 %s 不存在，且脚本中尚未创建该表", statement.Table.Name.O)
		}
		expected, err := migrationColumnDefinition(column, state.Definition, false)
		if err != nil {
			return err
		}
		name := strings.ToLower(column.Name.Name.O)
		actual, exists := state.Definition.Columns[name]
		object := statement.Table.Name.O + "." + column.Name.Name.O
		if exists && !reflect.DeepEqual(actual, expected) {
			return p.conflict("ADD_COLUMN", object, sql, "已有字段的类型、可空性或默认值与脚本不同")
		}
		p.step("ADD_COLUMN", object, sql, exists)
		if !exists {
			state.Definition.Columns[name] = expected
			state.Definition.Order = append(state.Definition.Order, name)
		}
	case *ast.CreateIndexStmt:
		if statement.KeyType != ast.IndexKeyTypeNone && statement.KeyType != ast.IndexKeyTypeUnique {
			return fmt.Errorf("仅支持普通索引和唯一索引")
		}
		state, err := p.table(statement.Table)
		if err != nil {
			return err
		}
		if state.Definition.Columns == nil {
			return fmt.Errorf("索引所属表 %s 不存在", statement.Table.Name.O)
		}
		expected, err := migrationIndexDefinition(statement.IndexPartSpecifications, statement.KeyType == ast.IndexKeyTypeUnique, statement.IndexOption)
		if err != nil {
			return err
		}
		name := strings.ToLower(statement.IndexName)
		object := statement.Table.Name.O + "." + statement.IndexName
		actual, exists := state.Definition.Indexes[name]
		if exists && !reflect.DeepEqual(actual, expected) {
			return p.conflict("CREATE_INDEX", object, sql, "同名索引的字段、顺序或唯一性与脚本不同")
		}
		for _, part := range expected.Parts {
			if _, ok := state.Definition.Columns[part.Name]; !ok {
				return p.conflict("CREATE_INDEX", object, sql, "索引引用的字段不存在")
			}
		}
		if !exists && expected.Unique && state.Exists {
			for _, part := range expected.Parts {
				if !state.OriginalColumns[part.Name] {
					return p.conflict("CREATE_INDEX", object, sql, "唯一索引依赖尚未创建的字段，无法提前核对重复数据")
				}
			}
			duplicate, err := p.uniqueConflict(statement.Table.Name.O, expected)
			if err != nil {
				return err
			}
			if duplicate {
				return p.conflict("CREATE_INDEX", object, sql, "已有重复数据，无法创建唯一索引；不会自动删除或合并数据")
			}
		}
		p.step("CREATE_INDEX", object, sql, exists)
		if !exists {
			state.Definition.Indexes[name] = expected
		}
	default:
		return fmt.Errorf("脚本包含建表、加字段、建索引以外的操作，不能自动补齐")
	}
	return nil
}

func (p *mysqlMigrationPlanner) uniqueConflict(table string, index migrationIndex) (bool, error) {
	identifier, err := sqlident.New(p.database, table)
	if err != nil {
		return false, err
	}
	quoted, err := identifier.SQL(sqlident.Backtick)
	if err != nil {
		return false, err
	}
	columns, nonNull := []string{}, []string{}
	for _, part := range index.Parts {
		identifier, err := sqlident.New("", part.Name)
		if err != nil {
			return false, err
		}
		column, err := identifier.SQL(sqlident.Backtick)
		if err != nil {
			return false, err
		}
		columns = append(columns, column)
		nonNull = append(nonNull, column+" IS NOT NULL")
	}
	var duplicates []int
	err = p.db.Raw("SELECT 1 FROM " + quoted + " WHERE " + strings.Join(nonNull, " AND ") + " GROUP BY " + strings.Join(columns, ", ") + " HAVING COUNT(*) > 1 LIMIT 1").Scan(&duplicates).Error
	return len(duplicates) > 0, err
}
