package service

import (
	"path/filepath"
	"testing"

	"cylawcase/internal/database"
	"cylawcase/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// newTestDB 打开一个真实的 SQLite 数据库（文件，WAL 模式）并迁移全部相关表，
// 用真实的事务、唯一约束与 CHECK 约束验证资金台账的并发正确性。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "fund_test.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)&_time_format=sqlite"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Client{}, &model.Case{}, &model.Billing{},
		&model.FundAccount{}, &model.FundEntry{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	if err := database.EnsureFundIndexes(db); err != nil {
		t.Fatalf("ensure fund indexes: %v", err)
	}
	// 放大连接池以真正制造并发事务。
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(16)
	return db
}

// seedCaseClient 写入一个客户与其案件，返回 (caseID, clientID)。
func seedCaseClient(t *testing.T, db *gorm.DB) (uint64, uint64) {
	t.Helper()
	client := &model.Client{Name: "测试客户"}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	cs := &model.Case{CaseNo: "CASE-TEST-1", Title: "测试案件", ClientID: client.ID, LeadLawyerID: 1}
	if err := db.Create(cs).Error; err != nil {
		t.Fatalf("seed case: %v", err)
	}
	return cs.ID, client.ID
}
