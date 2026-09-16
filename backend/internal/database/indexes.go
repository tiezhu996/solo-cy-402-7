package database

import (
	"fmt"

	"gorm.io/gorm"
)

// EnsureFundIndexes 创建资金台账所需、GORM 标签无法表达的「部分唯一索引」：
//
//	uniq_fund_entry_reversal ON fund_entries (reversal_of_id) WHERE reversal_of_id <> 0
//
// 该索引保证每条已入账明细最多被一笔冲销明细引用，从数据库层面杜绝重复冲销、余额被污染。
// 对 PostgreSQL 与 SQLite 均使用 CREATE UNIQUE INDEX ... WHERE 语法，且幂等。
func EnsureFundIndexes(db *gorm.DB) error {
	if db.Dialector.Name() == "sqlite" {
		// SQLite 不支持 IF NOT EXISTS 以外的差异；统一写法即可。
		stmt := "CREATE UNIQUE INDEX IF NOT EXISTS uniq_fund_entry_reversal ON fund_entries (reversal_of_id) WHERE reversal_of_id <> 0"
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("ensure fund reversal partial unique index: %w", err)
		}
		return nil
	}
	// PostgreSQL：先判存在再创建，保证幂等、不抛错。
	var exists bool
	check := `SELECT EXISTS (
		SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = 'uniq_fund_entry_reversal' AND n.nspname = current_schema())`
	if err := db.Raw(check).Scan(&exists).Error; err != nil {
		return fmt.Errorf("check fund reversal index: %w", err)
	}
	if exists {
		return nil
	}
	stmt := "CREATE UNIQUE INDEX uniq_fund_entry_reversal ON fund_entries (reversal_of_id) WHERE reversal_of_id <> 0"
	if err := db.Exec(stmt).Error; err != nil {
		return fmt.Errorf("create fund reversal partial unique index: %w", err)
	}
	return nil
}
