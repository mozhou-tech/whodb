# Diagnose and repair migrations

The migration tool's **Diagnose and repair** action reads Flyway history and
validates the selected source and directory. It shows failed migration versions,
validation messages, and the corresponding SQL. Diagnosis does not run migration
scripts or SQL/Java callbacks.

A repair plan is offered only when validation reports failed versioned migrations,
all of those versions have failed history records, and each failed script resolves
to one local file. Checksum mismatches, missing applied scripts, repeatable
migration failures, and other validation conflicts block this workflow.

## 已有表、字段或索引的恢复

MySQL 插件会只读解析当前失败版本的 SQL，并与实际数据库结构对比。
目前支持普通 `CREATE TABLE`、单字段 `ALTER TABLE ... ADD COLUMN`，以及
普通或唯一 `CREATE INDEX`。MariaDB/MySQL 兼容插件继承该核对器；无法可靠解析
或核对的结构会阻止自动补齐。实际数据库验收覆盖 MySQL 8.4。

弹窗逐项显示“已匹配”“待补齐”“冲突”：

- 已有对象的定义匹配时跳过；字段类型、可空性、默认值、主键、索引、字符集和
  排序规则不一致时阻止自动处理。
- 缺失的操作按脚本顺序补齐；唯一索引创建前检查现有重复数据，遵循 MySQL
  对 NULL 的唯一性规则，不自动删除或合并数据。
- 删除、修改字段、DML、外键、检查约束、分区、前缀/表达式索引及无法可靠核对的
  特殊 SQL 模式不进入自动补齐流程。

用户确认计划后，恢复流程固定使用已审核的 SQL 文件副本：

1. `repair` 清除当前失败记录，再 `validate`。
2. 若有已匹配的操作，仅在临时副本中保留缺失的语句；原始文件不变。
3. 运行 `migrate -target=<失败版本>` 一次，不执行之后的其他待迁移版本。
4. 再次核对实际结构，确认原脚本中的所有操作已满足。
5. 对照原始文件校验。只有校验和差异恰好属于本次恢复的版本时，才运行
   `repair` 对齐原始校验和；出现其他版本差异立即停止。
6. 最后再次用原始文件 `validate`。后续正常迁移不会因为临时副本而出现
   checksum 不一致。

其他连接器仍采用迁移历史修复流程：用户先核对并处理部分变更，再确认
`repair → validate → migrate`。它不会自动修改未知的表结构。

Any failed step stops the workflow. Each step's output remains available in the
dialog. Recovery uses an isolated configuration with the selected database/schema
and Flyway's default history table (`flyway_schema_history`); project configuration
and `FLYWAY_*` environment overrides are not loaded. SQL/Java callbacks are disabled.
Only pending migrations are ignored during validation, so checksum/history
conflicts cannot silently qualify for repair. Custom history tables or project
placeholder/configuration requirements need a separately reviewed manual workflow.

Plans expire after ten minutes, are single-use, and are bound to the source
credentials, database/schema, options, history, scanned SQL/configuration, and verified schema metadata.
Changing any of these before repair requires a new diagnosis. Once confirmed,
recovery runs against a private snapshot of the reviewed files throughout. Closing the dialog does not
execute a repair. Neither GraphQL operation allows anonymous access.

## Verification

```sh
cd core
# Unit/race checks and HTTP authorization regressions
go test -race ./src/flyway ./src/router

# Optional: installed Flyway with its bundled H2 driver, using temporary databases
WHODB_TEST_FLYWAY_BINARY=/opt/homebrew/bin/flyway \
  go test -v ./src/flyway -run 'TestLiveFlywayRecovery|TestFlywayDeduplicatedExecution' -count=1
```

The live recovery test creates a deliberately failed migration in a disposable H2
file database, fixes its script, diagnoses the failed record, repairs it with
explicit confirmation, validates and migrates once, then verifies a healthy
state. Invalid callback scripts prove the recovery flow does not execute them.

## MySQL 8.4 实测

在独立临时 MySQL 实例上运行（测试会创建并清理带专用前缀的数据库）：

```sh
cd core
WHODB_TEST_RECOVERY_MYSQL_ADDR=127.0.0.1:13307 \
WHODB_TEST_FLYWAY_BINARY=/opt/homebrew/bin/flyway \
  go test -race ./src/flyway ./src/plugins/mysql \
  -run 'TestLiveMySQLStructuralRecovery|TestLiveMigrationRecovery|TestLiveRecovery|TestStructural' -count=1
```

测试复现已有表错误，验证已有数据保留、缺失结构补齐、全部对象已存在时的空操作恢复、
原始文件不变和原始校验和通过，并验证未执行后续版本。还覆盖结构差异、重复数据、
标识符引用、字符集排序规则、JSON 与复合主键、元数据过期和其他版本校验和冲突阻断。

SQL AST 使用固定版本的 TiDB 独立解析器；实现依据为该版本的
[ast/ddl.go](https://github.com/pingcap/tidb/blob/b98174386ec5/pkg/parser/ast/ddl.go)（DDL 节点）、
[types/field_type.go](https://github.com/pingcap/tidb/blob/b98174386ec5/pkg/parser/types/field_type.go)（字段属性）和
[format/format.go](https://github.com/pingcap/tidb/blob/b98174386ec5/pkg/parser/format/format.go)
（标识符/字面量还原规则），保留无法核对的语句作为冲突，不用字符串替换猜测 SQL。
