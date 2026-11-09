package mysql

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/plugins"
	"github.com/clidey/whodb/core/src/sqlident"
	"gorm.io/gorm"
)

// CleanMigrationDuplicates retains the first or last primary key in each duplicate group atomically.
// A nil fingerprint previews without writes; applying requires an identical locked snapshot.
func (p *MySQLPlugin) CleanMigrationDuplicates(config *engine.PluginConfig, step engine.MigrationRecoveryStep, keepLast bool, fingerprint *string) (*engine.MigrationDuplicateCleanup, error) {
	if step.DuplicateTable == "" || len(step.DuplicateColumns) == 0 {
		return nil, errors.New("该冲突不支持重复数据清理")
	}
	if fingerprint != nil && (*fingerprint == "" || config.ReadOnly) {
		return nil, errors.New("清理需要有效预览，且连接必须允许写入")
	}
	table, err := cleanupIdentifier(config.Credentials.Database, step.DuplicateTable)
	if err != nil {
		return nil, err
	}
	return plugins.WithConnection(config, p.DB, func(db *gorm.DB) (*engine.MigrationDuplicateCleanup, error) {
		result := &engine.MigrationDuplicateCleanup{}
		err := db.Transaction(func(tx *gorm.DB) error {
			var storage string
			if err := tx.Raw("SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=?", config.Credentials.Database, step.DuplicateTable).Scan(&storage).Error; err != nil {
				return err
			}
			if !strings.EqualFold(storage, "InnoDB") {
				return errors.New("重复数据清理仅支持 InnoDB 事务表")
			}
			if err := tx.Raw("SELECT COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=? AND TABLE_NAME=? AND CONSTRAINT_NAME='PRIMARY' ORDER BY ORDINAL_POSITION", config.Credentials.Database, step.DuplicateTable).Scan(&result.PrimaryKeyColumns).Error; err != nil {
				return err
			}
			if len(result.PrimaryKeyColumns) == 0 {
				return errors.New("该表没有主键，无法可靠定义第一条或最后一条")
			}
			pk, keys, nulls, equals := []string{}, []string{}, []string{}, []string{}
			for _, name := range result.PrimaryKeyColumns {
				col, err := cleanupIdentifier("", name)
				if err != nil {
					return err
				}
				pk = append(pk, col)
			}
			for _, name := range step.DuplicateColumns {
				col, err := cleanupIdentifier("", name)
				if err != nil {
					return err
				}
				keys = append(keys, col)
				nulls = append(nulls, col+" IS NOT NULL")
				equals = append(equals, "k."+col+" = t."+col)
			}
			// Lock the full primary-key range during apply, including gaps that could admit new duplicates.
			snapshotSQL := "SELECT * FROM " + table + " ORDER BY " + strings.Join(pk, ", ")
			if fingerprint != nil {
				snapshotSQL += " FOR UPDATE"
			}
			snapshot, err := cleanupRawRows(tx, snapshotSQL)
			if err != nil {
				return err
			}
			schemaRows, err := cleanupRawRows(tx, "SHOW CREATE TABLE "+table)
			if err != nil {
				return err
			}
			var sideEffects int64
			if err := tx.Raw("SELECT COUNT(*) FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE UNIQUE_CONSTRAINT_SCHEMA=? AND REFERENCED_TABLE_NAME=? AND DELETE_RULE IN ('CASCADE','SET NULL')", config.Credentials.Database, step.DuplicateTable).Scan(&sideEffects).Error; err != nil {
				return err
			}
			if sideEffects > 0 {
				return errors.New("该表存在删除级联或置空外键，需人工处理关联数据")
			}
			if err := tx.Raw("SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=? AND EVENT_OBJECT_TABLE=? AND EVENT_MANIPULATION='DELETE'", config.Credentials.Database, step.DuplicateTable).Scan(&sideEffects).Error; err != nil {
				return err
			}
			if sideEffects > 0 {
				return errors.New("该表存在删除触发器，需人工核对后清理")
			}
			snapshotBytes, err := json.Marshal([]any{config.Credentials, step.SQL, step.DuplicateColumns, keepLast, schemaRows, snapshot})
			if err != nil {
				return err
			}
			sum := sha256.Sum256(snapshotBytes)
			result.Fingerprint = hex.EncodeToString(sum[:])
			if fingerprint != nil && result.Fingerprint != *fingerprint {
				return errors.New("数据或表结构已变化，请重新预览后确认清理")
			}
			grouped := "SELECT " + strings.Join(keys, ", ") + " FROM " + table + " WHERE " + strings.Join(nulls, " AND ") + " GROUP BY " + strings.Join(keys, ", ") + " HAVING COUNT(*) > 1"
			if err := tx.Raw("SELECT COUNT(*) FROM (" + grouped + ") AS duplicate_groups").Scan(&result.Groups).Error; err != nil {
				return err
			}
			comparison := "<"
			if keepLast {
				comparison = ">"
			}
			earlier, prefix, selected, predicates := []string{}, []string{}, []string{}, []string{}
			for _, col := range pk {
				clause := append(append([]string{}, prefix...), "k."+col+" "+comparison+" t."+col)
				earlier = append(earlier, "("+strings.Join(clause, " AND ")+")")
				prefix = append(prefix, "k."+col+" = t."+col)
				selected = append(selected, "t."+col)
				predicates = append(predicates, col+" = ?")
			}
			candidates, err := cleanupRawRows(tx, "SELECT "+strings.Join(selected, ", ")+" FROM "+table+" AS t WHERE EXISTS (SELECT 1 FROM "+table+" AS k WHERE "+strings.Join(equals, " AND ")+" AND ("+strings.Join(earlier, " OR ")+")) ORDER BY "+strings.Join(selected, ", "))
			if err != nil {
				return err
			}
			result.RowsToDelete = len(candidates)
			if fingerprint == nil {
				return nil
			}
			for _, row := range candidates {
				values := make([]any, len(row))
				for i, value := range row {
					values[i] = string(value)
				}
				deleted := tx.Exec("DELETE FROM "+table+" WHERE "+strings.Join(predicates, " AND "), values...)
				if deleted.Error != nil {
					return deleted.Error
				}
				if deleted.RowsAffected != 1 {
					return errors.New("记录已变化，清理事务已回滚")
				}
				result.Deleted++
			}
			var remaining int64
			if err := tx.Raw("SELECT COUNT(*) FROM (" + grouped + ") AS duplicate_groups").Scan(&remaining).Error; err != nil {
				return err
			}
			if remaining != 0 {
				return errors.New("重复数据核对未通过，清理事务已回滚")
			}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: fingerprint == nil})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}

func cleanupIdentifier(schema, name string) (string, error) {
	identifier, err := sqlident.New(schema, name)
	if err != nil {
		return "", err
	}
	return identifier.SQL(sqlident.Backtick)
}

func cleanupRawRows(db *gorm.DB, query string) ([][][]byte, error) {
	rows, err := db.Raw(query).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := [][][]byte{}
	for rows.Next() {
		values := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make([][]byte, len(values))
		for i, value := range values {
			if value != nil {
				row[i] = append([]byte{}, value...)
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
