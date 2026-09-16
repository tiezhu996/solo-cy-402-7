package service

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"cylawcase/internal/constants"
	"cylawcase/internal/model"
	"cylawcase/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// newMultiInstanceDB 打开一个共享文件库，供多个「服务实例」（各自独立的仓储/互斥锁）并发使用，
// 模拟多副本部署：进程内 writeMu 不共享，正确性只能依赖数据库约束与事务。
func newMultiInstanceDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "multi.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(15000)&_pragma=foreign_keys(ON)&_time_format=sqlite"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open multi db: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Client{}, &model.Case{}, &model.Billing{},
		&model.FundAccount{}, &model.FundEntry{}); err != nil {
		t.Fatalf("migrate multi db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(16)
	seedCaseClient(t, db)
	return db
}

// newInstance 在同一个库上构造一个独立的 FundService（独立 writeMu），代表一个服务实例。
func newInstance(db *gorm.DB) *FundService {
	return NewFundService(
		repository.NewFundRepository(db),
		repository.NewCaseRepository(db),
		repository.NewClientRepository(db),
		testLogger(),
	)
}

func accountCount(t *testing.T, db *gorm.DB, caseID, clientID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&model.FundAccount{}).
		Where("case_id = ? AND client_id = ?", caseID, clientID).Count(&n).Error; err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	return n
}

// 多实例并发首笔预收（不同幂等键）：两笔都必须成功，专案账户恰好只建一个。
func TestMultiInstanceConcurrentFirstPrepayment(t *testing.T) {
	db := newMultiInstanceDB(t)
	var cs model.Case
	if err := db.First(&cs).Error; err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	var ok, replay, fail int64
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			svc := newInstance(db) // 每个 goroutine 一个独立实例
			_, replayed, err := svc.RegisterPrepayment(cs.ID, cs.ClientID, int64(100+i), "首笔预收", "",
				"inst-pre-"+strconv.Itoa(i), testOp)
			mu.Lock()
			switch {
			case err != nil:
				fail++
			case replayed:
				replay++
			default:
				ok++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if fail != 0 {
		t.Fatalf("got %d failed first prepayments (must all succeed)", fail)
	}
	if ok != n || replay != 0 {
		t.Fatalf("ok=%d replay=%d fail=%d, want %d/0/0", ok, replay, fail, n)
	}
	if got := accountCount(t, db, cs.ID, cs.ClientID); got != 1 {
		t.Fatalf("account count = %d, want exactly 1", got)
	}
	// 余额 = 100+101+...+107 = 828
	if got := mustBalance(t, newInstance(db), cs.ID, cs.ClientID); got != 828 {
		t.Fatalf("balance = %d, want 828", got)
	}
	if entries := countEntries(t, db); entries != n {
		t.Fatalf("entries = %d, want %d", entries, n)
	}
}

// 多实例并发首笔预收（相同幂等键）：只入账一次，其余回放，账户只有一个，无内部错误。
func TestMultiInstanceConcurrentSameKeyFirstPrepayment(t *testing.T) {
	db := newMultiInstanceDB(t)
	var cs model.Case
	db.First(&cs)

	const n = 8
	var wg sync.WaitGroup
	var ok, replay, fail int64
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			svc := newInstance(db)
			_, replayed, err := svc.RegisterPrepayment(cs.ID, cs.ClientID, 250, "同键", "", "same-inst-key", testOp)
			mu.Lock()
			switch {
			case err != nil:
				fail++
			case replayed:
				replay++
			default:
				ok++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if fail != 0 {
		t.Fatalf("got %d failures (idempotent same-key must not error)", fail)
	}
	if ok != 1 || replay != n-1 {
		t.Fatalf("ok=%d replay=%d, want 1/%d", ok, replay, n-1)
	}
	if got := accountCount(t, db, cs.ID, cs.ClientID); got != 1 {
		t.Fatalf("account count = %d, want 1", got)
	}
	if got := mustBalance(t, newInstance(db), cs.ID, cs.ClientID); got != 250 {
		t.Fatalf("balance = %d, want 250 (posted once)", got)
	}
}

// 多实例并发首笔支出（账户尚不存在、余额为 0）：全部因余额不足拒绝，账户不被污染。
func TestMultiInstanceConcurrentFirstExpenseInsufficient(t *testing.T) {
	db := newMultiInstanceDB(t)
	var cs model.Case
	db.First(&cs)

	const n = 6
	var wg sync.WaitGroup
	var denied, other int64
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			svc := newInstance(db)
			_, _, err := svc.RegisterExpense(cs.ID, cs.ClientID, 50, "首笔支出", "", "inst-exp-"+strconv.Itoa(i), testOp)
			mu.Lock()
			if code := appErrorCodeErr(err); code == constants.CodeFundInsufficient {
				denied++
			} else if err != nil {
				other++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if other != 0 || denied != n {
		t.Fatalf("denied=%d other=%d, want %d/0 (no internal errors)", denied, other, n)
	}
	if n := countEntries(t, db); n != 0 {
		t.Fatalf("entries = %d, want 0", n)
	}
	if got := accountCount(t, db, cs.ID, cs.ClientID); got != 0 {
		t.Fatalf("account count = %d, want 0 (rolled back)", got)
	}
}

// 多实例下：先并发首笔预收建账户，随后并发支出争抢既有余额，结果与单实例一致、绝不为负。
func TestMultiInstanceExpenseAgainstExistingBalance(t *testing.T) {
	db := newMultiInstanceDB(t)
	var cs model.Case
	db.First(&cs)

	mustPrepay(t, newInstance(db), cs.ID, cs.ClientID, 1000, "seed-fund")

	const n = 20
	const each = 300
	var wg sync.WaitGroup
	var allowed, denied, other int64
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			svc := newInstance(db)
			_, _, err := svc.RegisterExpense(cs.ID, cs.ClientID, each, "支出", "", "mexp-"+strconv.Itoa(i), testOp)
			mu.Lock()
			switch code := appErrorCodeErr(err); {
			case err == nil:
				allowed++
			case code == constants.CodeFundInsufficient:
				denied++
			default:
				other++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if other != 0 {
		t.Fatalf("got %d non-insufficient errors", other)
	}
	if allowed != 3 || denied != 17 {
		t.Fatalf("allowed=%d denied=%d, want 3/17", allowed, denied)
	}
	if got := mustBalance(t, newInstance(db), cs.ID, cs.ClientID); got != 100 {
		t.Fatalf("balance = %d, want 100 (never negative)", got)
	}
	if got := accountCount(t, db, cs.ID, cs.ClientID); got != 1 {
		t.Fatalf("account count = %d, want 1", got)
	}
}

// 多实例并发冲销同一条预收：仅一笔成功，其余明确「已冲销」，无内部错误，余额不被重复冲销。
func TestMultiInstanceConcurrentReversal(t *testing.T) {
	db := newMultiInstanceDB(t)
	var cs model.Case
	db.First(&cs)
	origin := mustPrepay(t, newInstance(db), cs.ID, cs.ClientID, 1000, "origin-pre")

	const n = 8
	var wg sync.WaitGroup
	var ok, already, other int64
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			svc := newInstance(db)
			_, _, err := svc.ReverseEntry(origin.ID, "多实例冲销", "mrev-"+strconv.Itoa(i), testOp)
			mu.Lock()
			switch code := appErrorCodeErr(err); {
			case err == nil:
				ok++
			case code == constants.CodeFundAlreadyReversed:
				already++
			default:
				other++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if other != 0 {
		t.Fatalf("got %d unexpected errors: want none", other)
	}
	if ok != 1 || already != n-1 {
		t.Fatalf("ok=%d already=%d, want 1/%d", ok, already, n-1)
	}
	if got := mustBalance(t, newInstance(db), cs.ID, cs.ClientID); got != 0 {
		t.Fatalf("balance = %d, want 0", got)
	}
	if n := countEntries(t, db); n != 2 {
		t.Fatalf("entries = %d, want 2", n)
	}
}
