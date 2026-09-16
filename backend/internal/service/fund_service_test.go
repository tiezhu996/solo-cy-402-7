package service

import (
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"

	"cylawcase/internal/constants"
	"cylawcase/internal/model"
	"cylawcase/internal/repository"
	"cylawcase/internal/util"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newFundService 在已 seed（客户+案件）的库上构造资金台账服务，返回服务与 (caseID, clientID)。
func newFundService(db *gorm.DB) (*FundService, uint64, uint64) {
	var cs model.Case
	if err := db.First(&cs).Error; err != nil {
		panic(err)
	}
	svc := NewFundService(
		repository.NewFundRepository(db),
		repository.NewCaseRepository(db),
		repository.NewClientRepository(db),
		testLogger(),
	)
	return svc, cs.ID, cs.ClientID
}

var testOp = FundOperator{ID: 2, Name: "lawyer"}

func appErrorCode(t *testing.T, err error) int {
	t.Helper()
	var ae *util.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	t.Fatalf("expected AppError, got %v", err)
	return 0
}

func mustPrepay(t *testing.T, svc *FundService, caseID, clientID uint64, cents int64, key string) *model.FundEntry {
	t.Helper()
	e, replayed, err := svc.RegisterPrepayment(caseID, clientID, cents, "预收", "", key, testOp)
	if err != nil {
		t.Fatalf("prepay: %v", err)
	}
	if replayed {
		t.Fatalf("fresh prepay must not be replayed")
	}
	return e
}

func mustBalance(t *testing.T, svc *FundService, caseID, clientID uint64) int64 {
	t.Helper()
	computed, stored, consistent, err := svc.Balance(caseID, clientID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if !consistent {
		t.Fatalf("balance inconsistent: stored=%d recomputed=%d", stored, computed)
	}
	return stored
}

func countEntries(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&model.FundEntry{}).Count(&n).Error; err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return n
}

// 1. 预收入账，余额增加且与明细重算一致。
func TestPrepaymentPostsAndReconciles(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))

	mustPrepay(t, svc, caseID, clientID, 50000, "k-pre-1")
	if got := mustBalance(t, svc, caseID, clientID); got != 50000 {
		t.Fatalf("balance = %d, want 50000", got)
	}
}

// 2. 支出在余额内：扣减成功，余额不出负。
func TestExpenseWithinBalance(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))
	mustPrepay(t, svc, caseID, clientID, 10000, "k")

	if _, _, err := svc.RegisterExpense(caseID, clientID, 3000, "支出", "", "k-exp-1", testOp); err != nil {
		t.Fatalf("expense: %v", err)
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 7000 {
		t.Fatalf("balance = %d, want 7000", got)
	}
}

// 3. 余额不足：整笔拒绝，余额与明细均不变；随后更小金额可成功。
func TestExpenseInsufficientWholeReject(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))
	mustPrepay(t, svc, caseID, clientID, 10000, "k")
	beforeEntries := countEntries(t, db)

	_, _, err := svc.RegisterExpense(caseID, clientID, 12000, "超额支出", "", "k-exp-big", testOp)
	if code := appErrorCode(t, err); code != constants.CodeFundInsufficient {
		t.Fatalf("code = %d, want %d", code, constants.CodeFundInsufficient)
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 10000 {
		t.Fatalf("balance changed after reject: %d, want 10000", got)
	}
	if after := countEntries(t, db); after != beforeEntries {
		t.Fatalf("entries changed after reject: before=%d after=%d", beforeEntries, after)
	}
	// 余额足够的支出仍可成功，证明原明细未被污染。
	if _, _, err := svc.RegisterExpense(caseID, clientID, 10000, "全额支出", "", "k-exp-ok", testOp); err != nil {
		t.Fatalf("expense after reject should succeed: %v", err)
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 0 {
		t.Fatalf("balance = %d, want 0", got)
	}
}

// 4. 同一笔预收重复提交（相同幂等键）只入账一次，返回首次结果。
func TestIdempotentDuplicateSubmission(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))

	first := mustPrepay(t, svc, caseID, clientID, 5000, "same-key")
	for i := 0; i < 3; i++ {
		e, replayed, err := svc.RegisterPrepayment(caseID, clientID, 5000, "预收", "不同备注", "same-key", testOp)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if !replayed || e.ID != first.ID {
			t.Fatalf("replay %d must return canonical entry %d, got id=%d replayed=%v", i, first.ID, e.ID, replayed)
		}
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 5000 {
		t.Fatalf("balance = %d, want 5000 (posted once)", got)
	}
	if n := countEntries(t, db); n != 1 {
		t.Fatalf("entries = %d, want 1", n)
	}
}

// 5. 同一幂等键并发提交：恰好一笔入账。
func TestConcurrentSameKeyPostsOnce(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))

	const n = 16
	var wg sync.WaitGroup
	var success, replay int64
	var mu sync.Mutex
	ids := map[uint64]int{}
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			e, replayed, err := svc.RegisterPrepayment(caseID, clientID, 900, "并发预收", "", "concurrent-key", testOp)
			if err != nil {
				t.Errorf("concurrent prepay: %v", err)
				return
			}
			mu.Lock()
			ids[e.ID]++
			if replayed {
				replay++
			} else {
				success++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if success != 1 || replay != n-1 {
		t.Fatalf("success=%d replay=%d, want 1/%d", success, replay, n-1)
	}
	if len(ids) != 1 {
		t.Fatalf("got %d distinct entry ids, want 1", len(ids))
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 900 {
		t.Fatalf("balance = %d, want 900", got)
	}
}

// 6. 并发多笔支出争抢余额：余额绝不为负，入账总额不超过预收，被拒者整笔无副作用。
func TestConcurrentExpensesNeverOverdraw(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))
	mustPrepay(t, svc, caseID, clientID, 1000, "fund")

	const n = 20
	const each = 300
	var wg sync.WaitGroup
	var allowed, denied int64
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, _, err := svc.RegisterExpense(caseID, clientID, each, "并发支出", "", keyFor("exp", i), testOp)
			mu.Lock()
			if err == nil {
				allowed++
			} else if appErrorCodeErr(err) == constants.CodeFundInsufficient {
				denied++
			} else {
				t.Errorf("unexpected err: %v", err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if allowed != 3 || denied != 17 {
		t.Fatalf("allowed=%d denied=%d, want 3/17", allowed, denied)
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 100 {
		t.Fatalf("balance = %d, want 100 (never negative)", got)
	}
	// 1 预收 + 3 支出
	if n := countEntries(t, db); n != 4 {
		t.Fatalf("entries = %d, want 4", n)
	}
}

// 7. 同一明细并发重复冲销：仅一笔成功，其余拒绝；余额不被重复冲销污染。
func TestConcurrentReversalOnlyOnce(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))
	origin := mustPrepay(t, svc, caseID, clientID, 1000, "origin")

	const n = 12
	var wg sync.WaitGroup
	var success, denied int64
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, _, err := svc.ReverseEntry(origin.ID, "录错冲销", keyFor("rev", i), testOp)
			mu.Lock()
			if err == nil {
				success++
			} else if appErrorCodeErr(err) == constants.CodeFundAlreadyReversed {
				denied++
			} else {
				t.Errorf("unexpected reversal err: %v", err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if success != 1 || denied != n-1 {
		t.Fatalf("success=%d denied=%d, want 1/%d", success, denied, n-1)
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 0 {
		t.Fatalf("balance = %d, want 0 (reversed once)", got)
	}
	// 原明细 + 唯一一笔冲销
	if n := countEntries(t, db); n != 2 {
		t.Fatalf("entries = %d, want 2", n)
	}
	// 串行再冲销一次（新键）仍必须被拒。
	_, _, err := svc.ReverseEntry(origin.ID, "再次冲销", "rev-again", testOp)
	if code := appErrorCode(t, err); code != constants.CodeFundAlreadyReversed {
		t.Fatalf("second sequential reverse code=%d, want %d", code, constants.CodeFundAlreadyReversed)
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 0 {
		t.Fatalf("balance after double reverse = %d, want 0", got)
	}
}

// 8. 原记录不可改删：冲销后原明细 delta/余额快照保持不变，仅追加反向明细。
func TestReversalKeepsOriginImmutable(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))
	origin := mustPrepay(t, svc, caseID, clientID, 1000, "origin")
	originSnap := origin.BalanceCents

	if _, _, err := svc.RegisterExpense(caseID, clientID, 300, "支出", "", "exp", testOp); err != nil {
		t.Fatalf("expense: %v", err)
	}
	if _, _, err := svc.ReverseEntry(origin.ID, "冲销预收", "rev", testOp); err != nil {
		// 余额不足时会拒绝；本案余额 700，冲销预收需 -1000，应被拒。改测冲销支出。
		if appErrorCodeErr(err) != constants.CodeFundInsufficient {
			t.Fatalf("unexpected: %v", err)
		}
	}

	// 改为冲销支出（+300），原支出明细必须保持不变。
	var expEntry model.FundEntry
	if err := db.Where("entry_type = ?", constants.FundEntryExpense).First(&expEntry).Error; err != nil {
		t.Fatalf("find expense: %v", err)
	}
	rev, _, err := svc.ReverseEntry(expEntry.ID, "冲销支出", "rev-exp", testOp)
	if err != nil {
		t.Fatalf("reverse expense: %v", err)
	}
	if rev.DeltaCents != -expEntry.DeltaCents {
		t.Fatalf("reversal delta=%d, want %d", rev.DeltaCents, -expEntry.DeltaCents)
	}
	var reloaded model.FundEntry
	if err := db.First(&reloaded, expEntry.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.DeltaCents != expEntry.DeltaCents || reloaded.BalanceCents != expEntry.BalanceCents {
		t.Fatalf("origin expense mutated: delta %d->%d snap %d->%d",
			expEntry.DeltaCents, reloaded.DeltaCents, expEntry.BalanceCents, reloaded.BalanceCents)
	}
	if reloaded.ReversedByID != rev.ID {
		t.Fatalf("reversed_by_id = %d, want %d", reloaded.ReversedByID, rev.ID)
	}
	if got := mustBalance(t, svc, caseID, clientID); got != originSnap {
		t.Fatalf("balance after reversing expense = %d, want %d", got, originSnap)
	}
}

// 9. 冲销预收款但当前余额不足：拒绝，余额与明细不变。
func TestReversePrepaymentInsufficientDenied(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))
	origin := mustPrepay(t, svc, caseID, clientID, 1000, "origin")
	if _, _, err := svc.RegisterExpense(caseID, clientID, 800, "支出", "", "exp", testOp); err != nil {
		t.Fatalf("expense: %v", err)
	}
	before := countEntries(t, db)
	_, _, err := svc.ReverseEntry(origin.ID, "想冲销预收", "rev", testOp)
	if code := appErrorCode(t, err); code != constants.CodeFundInsufficient {
		t.Fatalf("code=%d, want %d", code, constants.CodeFundInsufficient)
	}
	if got := mustBalance(t, svc, caseID, clientID); got != 200 {
		t.Fatalf("balance = %d, want 200", got)
	}
	if countEntries(t, db) != before {
		t.Fatalf("entries must not change on denied reversal")
	}
}

// 10. 不能冲销一笔冲销明细。
func TestCannotReverseReversal(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))
	origin := mustPrepay(t, svc, caseID, clientID, 500, "origin")
	rev, _, err := svc.ReverseEntry(origin.ID, "冲销", "rev1", testOp)
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	_, _, err = svc.ReverseEntry(rev.ID, "冲销冲销", "rev2", testOp)
	if code := appErrorCode(t, err); code != constants.CodeValidationFailed {
		t.Fatalf("code=%d, want %d", code, constants.CodeValidationFailed)
	}
}

// 11. 每笔必须归属同一案件与其本人客户：客户与案件不匹配则拒绝。
func TestEntryMustBelongToCaseAndClient(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, _ := newFundService(seededDB(t, db))

	other := &model.Client{Name: "其他客户"}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other client: %v", err)
	}
	_, _, err := svc.RegisterPrepayment(caseID, other.ID, 100, "预收", "", "k-mismatch", testOp)
	if code := appErrorCode(t, err); code != constants.CodeValidationFailed {
		t.Fatalf("code=%d, want %d", code, constants.CodeValidationFailed)
	}
}

// 12. 重启后回读：新建仓储/服务（模拟进程重启），余额与明细一致且不丢不重。
func TestRestartReadbackConsistent(t *testing.T) {
	db := newTestDB(t)
	svc, caseID, clientID := newFundService(seededDB(t, db))
	mustPrepay(t, svc, caseID, clientID, 5000, "a")
	if _, _, err := svc.RegisterExpense(caseID, clientID, 1200, "支出", "", "b", testOp); err != nil {
		t.Fatalf("expense: %v", err)
	}
	if _, _, err := svc.RegisterExpense(caseID, clientID, 4000, "超额", "", "c", testOp); err == nil {
		t.Fatalf("overdraw must fail")
	}

	// 用同一数据库新建仓储/服务（模拟进程重启后的回读路径），余额与明细一致、不丢不重。
	restarted := NewFundService(
		repository.NewFundRepository(db),
		repository.NewCaseRepository(db),
		repository.NewClientRepository(db),
		testLogger(),
	)
	computed, stored, consistent, err := restarted.Balance(caseID, clientID)
	if err != nil {
		t.Fatalf("restart balance: %v", err)
	}
	if !consistent || stored != 3800 || computed != 3800 {
		t.Fatalf("after restart stored=%d computed=%d consistent=%v, want 3800/3800/true", stored, computed, consistent)
	}
	entries, err := restarted.ListEntriesByCase(caseID)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries after restart = %d, want 2", len(entries))
	}
}

// TestRestartFromClosedDB 关闭后重新打开同一文件，验证真正的磁盘持久化与回读。
func TestRestartFromClosedDB(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/fund.db"
	dsn := path + "?_pragma=busy_timeout(10000)&_time_format=sqlite"

	open := func() *gorm.DB {
		d, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if err := d.AutoMigrate(&model.User{}, &model.Client{}, &model.Case{},
			&model.FundAccount{}, &model.FundEntry{}); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		return d
	}

	d1 := open()
	client := &model.Client{Name: "持久化客户"}
	d1.Create(client)
	cs := &model.Case{CaseNo: "CASE-PERSIST", Title: "持久化案件", ClientID: client.ID, LeadLawyerID: 1}
	d1.Create(cs)
	svc1 := NewFundService(repository.NewFundRepository(d1), repository.NewCaseRepository(d1),
		repository.NewClientRepository(d1), testLogger())
	mustPrepay(t, svc1, cs.ID, client.ID, 7777, "persist-key")
	sql1, _ := d1.DB()
	_ = sql1.Close()

	d2 := open()
	svc2 := NewFundService(repository.NewFundRepository(d2), repository.NewCaseRepository(d2),
		repository.NewClientRepository(d2), testLogger())
	computed, stored, consistent, err := svc2.Balance(cs.ID, client.ID)
	if err != nil {
		t.Fatalf("reopen balance: %v", err)
	}
	if !consistent || stored != 7777 || computed != 7777 {
		t.Fatalf("reopen stored=%d computed=%d consistent=%v, want 7777", stored, computed, consistent)
	}
	// 幂等键在重启后仍然生效：重试同一笔不会二次入账。
	e, replayed, err := svc2.RegisterPrepayment(cs.ID, client.ID, 7777, "", "", "persist-key", testOp)
	if err != nil || !replayed || e.BalanceCents != 7777 {
		t.Fatalf("post-restart replay failed: err=%v replayed=%v", err, replayed)
	}
}

// helpers

func seededDB(t *testing.T, db *gorm.DB) *gorm.DB {
	t.Helper()
	seedCaseClient(t, db)
	return db
}

func keyFor(prefix string, i int) string {
	return prefix + "-" + strconv.Itoa(i)
}

func appErrorCodeErr(err error) int {
	var ae *util.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return -1
}
