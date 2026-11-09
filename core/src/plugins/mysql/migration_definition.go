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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/format"
	parsermysql "github.com/pingcap/tidb/pkg/parser/mysql"
	_ "github.com/pingcap/tidb/pkg/parser/test_driver"
)

type migrationDefaults struct {
	Engine, Charset, Collation, CharsetCollation string
	CharsetDefaults                              map[string]string
}

type migrationColumn struct {
	Type                                           byte
	Length, Scale                                  int
	Unsigned, Zerofill, Nullable, AutoIncrement    bool
	Charset, Collation, Default, OnUpdate, Comment string
	Elements                                       []string
}

type migrationIndexPart struct {
	Name string
	Desc bool
}
type migrationIndex struct {
	Unique  bool
	Parts   []migrationIndexPart
	Comment string
}
type migrationTable struct {
	Engine, Charset, Collation, Comment string
	RowFormat                           uint64
	Order                               []string
	Columns                             map[string]migrationColumn
	Indexes                             map[string]migrationIndex
}

func restoreMigrationNode(node ast.Node) (string, error) {
	var text strings.Builder
	if err := node.Restore(format.NewRestoreCtx(format.DefaultRestoreFlags, &text)); err != nil {
		return "", err
	}
	return text.String(), nil
}

func parseMigrationSQL(sql string) ([]ast.StmtNode, error) {
	if strings.Contains(sql, "${") || strings.Contains(sql, "/*!") || strings.Contains(sql, "/*T!") {
		return nil, fmt.Errorf("含占位符或条件执行注释的脚本需要人工处理")
	}
	nodes, warnings, err := parser.New().ParseSQL(sql)
	if err != nil || len(warnings) != 0 || len(nodes) == 0 {
		return nil, fmt.Errorf("无法完整解析迁移 SQL，仅支持普通建表、增加字段和创建索引")
	}
	return nodes, nil
}

func migrationDefaultValue(expr ast.ExprNode) (string, error) {
	if value, ok := expr.(ast.ValueExpr); ok {
		if value.GetValue() == nil {
			return "NULL", nil
		}
		// MySQL SHOW CREATE quotes numeric defaults that were originally literals.
		encoded, err := json.Marshal(fmt.Sprint(value.GetValue()))
		return string(encoded), err
	}
	return restoreMigrationNode(expr)
}

func migrationColumnDefinition(column *ast.ColumnDef, table migrationTable, primary bool, charsetDefaults map[string]string) (migrationColumn, error) {
	tp := column.Tp
	result := migrationColumn{Type: tp.GetType(), Length: tp.GetFlen(), Scale: tp.GetDecimal(), Unsigned: parsermysql.HasUnsignedFlag(tp.GetFlag()), Zerofill: parsermysql.HasZerofillFlag(tp.GetFlag()), Nullable: !primary, Elements: tp.GetElems()}
	switch result.Type {
	case parsermysql.TypeTiny, parsermysql.TypeShort, parsermysql.TypeLong, parsermysql.TypeLonglong, parsermysql.TypeInt24:
		result.Length = -1 // Integer display width is omitted by MySQL 8 SHOW CREATE.
	case parsermysql.TypeVarchar, parsermysql.TypeVarString, parsermysql.TypeString, parsermysql.TypeTinyBlob, parsermysql.TypeBlob, parsermysql.TypeMediumBlob, parsermysql.TypeLongBlob, parsermysql.TypeEnum, parsermysql.TypeSet:
		result.Charset, result.Collation = table.Charset, table.Collation
		if tp.GetCharset() != "" {
			charset := strings.ToLower(tp.GetCharset())
			collation, ok := charsetDefaults[charset]
			if !ok {
				return result, fmt.Errorf("无法获取字段 %s 的字符集 %s 默认排序规则", column.Name.Name.O, charset)
			}
			result.Charset, result.Collation = charset, collation
		}
		if tp.GetCollate() != "" {
			result.Collation = strings.ToLower(tp.GetCollate())
		}
	}
	hasDefault := false
	for _, option := range column.Options {
		switch option.Tp {
		case ast.ColumnOptionNull:
			result.Nullable = !primary
		case ast.ColumnOptionNotNull, ast.ColumnOptionPrimaryKey:
			if option.Tp == ast.ColumnOptionPrimaryKey && option.PrimaryKeyTp != ast.PrimaryKeyTypeDefault {
				return result, fmt.Errorf("特殊主键布局需要人工核对")
			}
			result.Nullable = false
		case ast.ColumnOptionAutoIncrement:
			result.AutoIncrement = true
		case ast.ColumnOptionDefaultValue:
			value, err := migrationDefaultValue(option.Expr)
			if err != nil {
				return result, err
			}
			result.Default, hasDefault = value, true
		case ast.ColumnOptionOnUpdate:
			value, err := restoreMigrationNode(option.Expr)
			if err != nil {
				return result, err
			}
			result.OnUpdate = value
		case ast.ColumnOptionComment:
			value, err := migrationDefaultValue(option.Expr)
			if err != nil {
				return result, err
			}
			result.Comment = value
		case ast.ColumnOptionCollate:
			collation := strings.ToLower(option.StrValue)
			if !(result.Charset == "binary" && collation == "binary") && !strings.HasPrefix(collation, result.Charset+"_") {
				return result, fmt.Errorf("字段 %s 的排序规则需要人工核对", column.Name.Name.O)
			}
			result.Collation = collation
		case ast.ColumnOptionUniqKey, ast.ColumnOptionNoOption:
		default:
			return result, fmt.Errorf("字段 %s 使用了自动对齐尚不支持的约束", column.Name.Name.O)
		}
	}
	if result.Nullable && !hasDefault {
		result.Default = "NULL"
	}
	return result, nil
}

func migrationIndexDefinition(parts []*ast.IndexPartSpecification, unique bool, option *ast.IndexOption) (migrationIndex, error) {
	result := migrationIndex{Unique: unique, Parts: []migrationIndexPart{}}
	if option != nil {
		if (option.Tp != ast.IndexTypeInvalid && option.Tp != ast.IndexTypeBtree) || option.Visibility == ast.IndexVisibilityInvisible || option.Condition != nil || option.KeyBlockSize != 0 || option.ParserName.O != "" || option.Global || option.SplitOpt != nil || option.SecondaryEngineAttr != "" || option.PrimaryKeyTp != ast.PrimaryKeyTypeDefault || option.AutoPreSplit || option.AddColumnarReplicaOnDemand != 0 {
			return result, fmt.Errorf("仅支持普通可见 BTREE 索引")
		}
		result.Comment = option.Comment
	}
	for _, part := range parts {
		if part.Expr != nil || part.Column == nil || part.Length > 0 || part.Column.Table.O != "" || part.Column.Schema.O != "" {
			return result, fmt.Errorf("暂不支持前缀、表达式或跨表索引")
		}
		result.Parts = append(result.Parts, migrationIndexPart{Name: strings.ToLower(part.Column.Name.O), Desc: part.Desc})
	}
	if len(result.Parts) == 0 {
		return result, fmt.Errorf("索引缺少字段")
	}
	return result, nil
}

func migrationTableDefinition(node *ast.CreateTableStmt, defaults migrationDefaults) (migrationTable, error) {
	result := migrationTable{Engine: defaults.Engine, Charset: defaults.Charset, Collation: defaults.Collation, Columns: map[string]migrationColumn{}, Indexes: map[string]migrationIndex{}}
	if node.TemporaryKeyword != ast.TemporaryNone || node.Select != nil || node.ReferTable != nil || node.Partition != nil || len(node.SplitIndex) != 0 {
		return result, fmt.Errorf("暂不支持临时表、分区表或从查询/其他表创建的表")
	}
	explicitCollation := ""
	for _, option := range node.Options {
		switch option.Tp {
		case ast.TableOptionEngine:
			result.Engine = strings.ToLower(option.StrValue)
		case ast.TableOptionCharset:
			if !strings.EqualFold(option.StrValue, defaults.Charset) {
				return result, fmt.Errorf("迁移字符集与数据库默认字符集不同，需要人工核对排序规则")
			}
			result.Charset = strings.ToLower(option.StrValue)
			result.Collation = defaults.CharsetCollation
		case ast.TableOptionCollate:
			explicitCollation = strings.ToLower(option.StrValue)
		case ast.TableOptionComment:
			result.Comment = option.StrValue
		case ast.TableOptionAutoIncrement:
		case ast.TableOptionRowFormat:
			switch option.UintValue {
			case ast.RowFormatDefault:
				result.RowFormat = 0
			case ast.RowFormatDynamic, ast.RowFormatFixed, ast.RowFormatCompressed, ast.RowFormatRedundant, ast.RowFormatCompact:
				result.RowFormat = option.UintValue
			default:
				return result, fmt.Errorf("该 ROW_FORMAT 行存储格式尚不支持自动核对")
			}
		default:
			text, err := restoreMigrationNode(option)
			if err != nil {
				return result, fmt.Errorf("无法核对表选项（类型 %d）：%w", option.Tp, err)
			}
			name, _, _ := strings.Cut(text, " ")
			return result, fmt.Errorf("表选项 %s（类型 %d）尚不支持自动核对", name, option.Tp)
		}
	}
	if explicitCollation != "" {
		result.Collation = explicitCollation
	}
	for _, constraint := range node.Constraints {
		unique, name := false, strings.ToLower(constraint.Name)
		switch constraint.Tp {
		case ast.ConstraintPrimaryKey:
			unique, name = true, "primary"
		case ast.ConstraintUniq, ast.ConstraintUniqKey, ast.ConstraintUniqIndex:
			unique = true
		case ast.ConstraintKey, ast.ConstraintIndex:
		default:
			return result, fmt.Errorf("含外键或检查约束的表需要人工核对")
		}
		index, err := migrationIndexDefinition(constraint.Keys, unique, constraint.Option)
		if err != nil {
			return result, err
		}
		if name == "" {
			name = index.Parts[0].Name
		}
		if _, exists := result.Indexes[name]; exists {
			return result, fmt.Errorf("脚本中存在同名索引")
		}
		result.Indexes[name] = index
	}
	for _, column := range node.Cols {
		name := strings.ToLower(column.Name.Name.O)
		primary := false
		for _, part := range result.Indexes["primary"].Parts {
			if part.Name == name {
				primary = true
			}
		}
		for _, option := range column.Options {
			if option.Tp == ast.ColumnOptionPrimaryKey {
				primary = true
				result.Indexes["primary"] = migrationIndex{Unique: true, Parts: []migrationIndexPart{{Name: name}}}
			}
			if option.Tp == ast.ColumnOptionUniqKey {
				result.Indexes[name] = migrationIndex{Unique: true, Parts: []migrationIndexPart{{Name: name}}}
			}
		}
		definition, err := migrationColumnDefinition(column, result, primary, defaults.CharsetDefaults)
		if err != nil {
			return result, err
		}
		if _, exists := result.Columns[name]; exists {
			return result, fmt.Errorf("脚本中存在同名字段")
		}
		result.Columns[name] = definition
		result.Order = append(result.Order, name)
	}
	return result, nil
}
