package repository

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cylawcase/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FundRepository 案件资金台账仓储。
//
// 正确性由两层保证：
//  1. 数据库层（权威、跨进程）：写事务内对专案账户行 SELECT ... FOR UPDATE 串行化；
//     余额非负由「条件 UPDATE ... WHERE balance_cents + ? >= 0」与 CHECK 约束双重兜底；
//     幂等键唯一索引保证重复提交/并发重试只入账一次；部分唯一索引 + reversed_by_id 抢占
//     保证同一明细最多被冲销一次。账户创建用 INSERT ... ON CONFLICT DO NOTHING，多实例并发
//     首笔也不会因唯一冲突导致事务 abort 而返回内部错误。
//  2. 进程层（补充、确定性）：Postgres 由行锁保证跨实例正确，仅用本仓储互斥减少无谓回滚；
//     SQLite 没有行锁，用包级全局互斥把所有资金写事务串行化（含不同仓储/服务实例），
//     避免其 deferred 事务的 SQLITE_BUSY_SNAPSHOT 写冲突。
type FundRepository struct {
	db      *gorm.DB
	writeMu sync.Mutex
}

// sqliteGlobalWriteMu 在 SQLite 下串行化全部资金写事务（跨不同仓储实例，模拟多实例）。
var sqliteGlobalWriteMu sync.Mutex

// beginWrite 取得写事务互斥，返回释放函数。
func (r *FundRepository) beginWrite() func() {
	if r.db.Dialector.Name() == "sqlite" {
		sqliteGlobalWriteMu.Lock()
		return sqliteGlobalWriteMu.Unlock
	}
	r.writeMu.Lock()
	return r.writeMu.Unlock
}

// NewFundRepository 构造资金台账仓储。
func NewFundRepository(db *gorm.DB) *FundRepository {
	return &FundRepository{db: db}
}

// DB 暴露底层连接（供需要独立事务的服务层使用）。
func (r *FundRepository) DB() *gorm.DB { return r.db }

// PostedResult 一次入账的结果。
type PostedResult struct {
	Entry     *model.FundEntry
	Replayed  bool // true 表示命中幂等键，返回的是已存在的那一笔（重试/并发去重）
	AccountID uint64
}

// forUpdate 对支持行锁的方言追加 SELECT ... FOR UPDATE；SQLite 忽略（其写事务天然串行）。
func forUpdate(tx *gorm.DB) *gorm.DB {
	if tx.Dialector.Name() == "sqlite" {
		return tx
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

// replayIfPresent 在事务失败后用全新读取按幂等键回查：
// 若另一并发事务已用同一幂等键入账，则返回该笔作为「幂等重放」，保证重复提交/并发重试只入账一次。
func (r *FundRepository) replayIfPresent(key string) (*PostedResult, bool) {
	if key == "" {
		return nil, false
	}
	e, err := r.FindEntryByIdempotencyKey(r.db, key)
	if err != nil || e == nil {
		return nil, false
	}
	return &PostedResult{Entry: e, Replayed: true, AccountID: e.AccountID}, true
}

// FindEntryByIdempotencyKey 按幂等键查找已入账明细；不存在返回 (nil, nil)。
func (r *FundRepository) FindEntryByIdempotencyKey(tx *gorm.DB, key string) (*model.FundEntry, error) {
	if key == "" {
		return nil, nil
	}
	var e model.FundEntry
	err := tx.Where("idempotency_key = ?", key).First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find fund entry by idempotency key: %w", err)
	}
	return &e, nil
}

// FindEntryByID 按主键查找明细；不存在返回 ErrNotFound。
func (r *FundRepository) FindEntryByID(tx *gorm.DB, id uint64) (*model.FundEntry, error) {
	var e model.FundEntry
	err := forUpdate(tx).First(&e, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find fund entry by id: %w", err)
	}
	return &e, nil
}

// lockAccount 按 (案件, 客户) 锁定并返回专案账户行。
func (r *FundRepository) lockAccount(tx *gorm.DB, caseID, clientID uint64) (*model.FundAccount, error) {
	var acc model.FundAccount
	err := forUpdate(tx).
		Where("case_id = ? AND client_id = ?", caseID, clientID).
		First(&acc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock fund account: %w", err)
	}
	return &acc, nil
}

// getOrCreateAccount 返回持锁的专案账户行；不存在则创建。
//
// 关键：使用 INSERT ... ON CONFLICT (case_id, client_id) DO NOTHING，而不是「先 Create 再捕获唯一冲突」。
// 在 PostgreSQL 中，事务一旦发生唯一约束冲突就会进入 aborted 状态、后续语句全部失败；
// 多个服务实例并发首笔记账时，落败的那笔即使捕获了错误也无法在同一事务里继续，最终整笔回滚、返回内部错误。
// ON CONFLICT DO NOTHING 在冲突时不报错、不污染事务，随后统一 FOR UPDATE 回读唯一账户行即可。
func (r *FundRepository) getOrCreateAccount(tx *gorm.DB, caseID, clientID uint64) (*model.FundAccount, error) {
	newAcc := &model.FundAccount{CaseID: caseID, ClientID: clientID, BalanceCents: 0}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "case_id"}, {Name: "client_id"}},
		DoNothing: true,
	}).Create(newAcc).Error; err != nil {
		return nil, fmt.Errorf("ensure fund account: %w", err)
	}
	// 无论本次是否真的插入，都重新加锁读取唯一账户行：
	// 并发下若账户由另一事务先建，这里的 FOR UPDATE 会阻塞至其提交后读到已提交的行。
	return r.lockAccount(tx, caseID, clientID)
}

// applyDelta 在持有的账户行上做带条件的原子增减。
// requireNonNegative=true（支出、反向冲销预收款）时，余额不足则条件不命中，返回 false，调用方整笔回滚。
// 账户行已在事务内加行锁，故无需乐观版本条件；version 仅作为修改次数计数递增。
func (r *FundRepository) applyDelta(tx *gorm.DB, accID uint64, delta int64, requireNonNegative bool) (bool, error) {
	q := tx.Model(&model.FundAccount{}).Where("id = ?", accID)
	if requireNonNegative {
		q = q.Where("balance_cents + ? >= 0", delta)
	}
	res := q.Updates(map[string]any{
		"balance_cents": gorm.Expr("balance_cents + ?", delta),
		"version":       gorm.Expr("version + 1"),
		"updated_at":    time.Now(),
	})
	if res.Error != nil {
		return false, fmt.Errorf("apply fund balance delta: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// reloadBalance 在同一事务/行锁内重新读取权威余额，避免使用可能过期的缓存值。
func (r *FundRepository) reloadBalance(tx *gorm.DB, accID uint64) (int64, error) {
	var bal int64
	if err := tx.Model(&model.FundAccount{}).Where("id = ?", accID).
		Select("balance_cents").Scan(&bal).Error; err != nil {
		return 0, fmt.Errorf("reload fund balance: %w", err)
	}
	return bal, nil
}

// insertEntry 追加一条只追加明细。唯一索引冲突（重复幂等键/重复冲销）由调用方据错误码处理。
func (r *FundRepository) insertEntry(tx *gorm.DB, e *model.FundEntry) error {
	if err := tx.Create(e).Error; err != nil {
		if IsDuplicateKeyErr(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert fund entry: %w", err)
	}
	return nil
}

// markReversalLink 记录「原明细 -> 冲销明细」的关联。
// 通过在原明细行写入 reversed_by_id（仅当其仍为 0）实现乐观抢占：
// 并发重复冲销时只有一笔 RowsAffected=1，另一笔为 0，从而被拒绝，绝不重复冲销、污染余额。
func (r *FundRepository) markReversalLink(tx *gorm.DB, originID, reversalID uint64) (bool, error) {
	res := tx.Model(&model.FundEntry{}).
		Where("id = ? AND reversal_of_id = 0 AND reversed_by_id = 0", originID).
		Update("reversed_by_id", reversalID)
	if res.Error != nil {
		return false, fmt.Errorf("mark reversal link: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// PostPrepayment 登记一笔客户预收款（delta 为正），整笔事务。
// entry.IdempotencyKey 必须由调用方保证非空。
func (r *FundRepository) PostPrepayment(entry *model.FundEntry) (*PostedResult, error) {
	if entry.DeltaCents <= 0 {
		return nil, fmt.Errorf("post prepayment: delta must be positive, got %d", entry.DeltaCents)
	}
	var result *PostedResult
	release := r.beginWrite()
	defer release()
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if existing, err := r.FindEntryByIdempotencyKey(tx, entry.IdempotencyKey); err != nil {
			return err
		} else if existing != nil {
			result = &PostedResult{Entry: existing, Replayed: true, AccountID: existing.AccountID}
			return nil
		}
		acc, err := r.getOrCreateAccount(tx, entry.CaseID, entry.ClientID)
		if err != nil {
			return err
		}
		// 拿到账户行锁后再查一次幂等键：多实例下另一实例可能已在锁等待期间提交同键首笔，
		// 此时必须回放该笔，绝不能再插入（否则会触发唯一冲突并污染事务）。
		if existing, err := r.FindEntryByIdempotencyKey(tx, entry.IdempotencyKey); err != nil {
			return err
		} else if existing != nil {
			result = &PostedResult{Entry: existing, Replayed: true, AccountID: existing.AccountID}
			return nil
		}
		if ok, err := r.applyDelta(tx, acc.ID, entry.DeltaCents, false); err != nil {
			return err
		} else if !ok {
			return ErrConflict
		}
		newBalance, err := r.reloadBalance(tx, acc.ID)
		if err != nil {
			return err
		}
		entry.AccountID = acc.ID
		entry.BalanceCents = newBalance
		if err := r.insertEntry(tx, entry); err != nil {
			return err
		}
		result = &PostedResult{Entry: entry, AccountID: acc.ID}
		return nil
	})
	if err != nil {
		if replay, ok := r.replayIfPresent(entry.IdempotencyKey); ok {
			return replay, nil
		}
		return nil, err
	}
	return result, nil
}

// PostExpense 登记一笔办案支出（delta 为负）。余额不足时条件不命中 → 返回 ErrInsufficient，
// 事务回滚，原明细与余额均不变（整笔拒绝）。
func (r *FundRepository) PostExpense(entry *model.FundEntry) (*PostedResult, error) {
	if entry.DeltaCents >= 0 {
		return nil, fmt.Errorf("post expense: delta must be negative, got %d", entry.DeltaCents)
	}
	var result *PostedResult
	release := r.beginWrite()
	defer release()
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if existing, err := r.FindEntryByIdempotencyKey(tx, entry.IdempotencyKey); err != nil {
			return err
		} else if existing != nil {
			result = &PostedResult{Entry: existing, Replayed: true, AccountID: existing.AccountID}
			return nil
		}
		acc, err := r.getOrCreateAccount(tx, entry.CaseID, entry.ClientID)
		if err != nil {
			return err
		}
		// 同预收：行锁后复查幂等键，避免多实例同键并发在等待锁后重复插入。
		if existing, err := r.FindEntryByIdempotencyKey(tx, entry.IdempotencyKey); err != nil {
			return err
		} else if existing != nil {
			result = &PostedResult{Entry: existing, Replayed: true, AccountID: existing.AccountID}
			return nil
		}
		ok, err := r.applyDelta(tx, acc.ID, entry.DeltaCents, true)
		if err != nil {
			return err
		}
		if !ok {
			// 专案可用余额不足：整笔拒绝，不写入任何明细。
			return ErrInsufficient
		}
		newBalance, err := r.reloadBalance(tx, acc.ID)
		if err != nil {
			return err
		}
		entry.AccountID = acc.ID
		entry.BalanceCents = newBalance
		if err := r.insertEntry(tx, entry); err != nil {
			return err
		}
		result = &PostedResult{Entry: entry, AccountID: acc.ID}
		return nil
	})
	if err != nil {
		if replay, ok := r.replayIfPresent(entry.IdempotencyKey); ok {
			return replay, nil
		}
		return nil, err
	}
	return result, nil
}

// PostReversal 对一条已入账明细做反向冲销，整笔事务：
//  1. 命中幂等键则直接返回原冲销明细；
//  2. 锁定并校验原明细（存在、未被冲销、且自身不是冲销明细）；
//  3. 锁定同一专案账户，反向 delta 条件扣减（冲销预收款会占用余额，余额不足则拒绝）；
//  4. 乐观抢占 reversed_by_id，并发重复冲销只有一笔成功；
//  5. 追加冲销明细。原明细永不修改、永不删除。
func (r *FundRepository) PostReversal(reversal *model.FundEntry, originID uint64) (*PostedResult, error) {
	var result *PostedResult
	release := r.beginWrite()
	defer release()
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if existing, err := r.FindEntryByIdempotencyKey(tx, reversal.IdempotencyKey); err != nil {
			return err
		} else if existing != nil {
			result = &PostedResult{Entry: existing, Replayed: true, AccountID: existing.AccountID}
			return nil
		}

		origin, err := r.FindEntryByID(tx, originID)
		if err != nil {
			return err // ErrNotFound 透传
		}
		if origin.IsReversal() {
			return ErrCannotReverseReversal
		}
		// 锁住原明细后复查幂等键：多实例下同键冲销并发，落败方在 FOR UPDATE 等待后，
		// 胜出方已提交该冲销明细，此时应回放首笔，而不是按「已冲销」拒绝。
		if existing, err := r.FindEntryByIdempotencyKey(tx, reversal.IdempotencyKey); err != nil {
			return err
		} else if existing != nil {
			result = &PostedResult{Entry: existing, Replayed: true, AccountID: existing.AccountID}
			return nil
		}
		// 行锁内若已被其他事务（用不同幂等键）冲销，立即拒绝（并发重复冲销的主路径），避免误判为余额不足。
		if origin.ReversedByID != 0 {
			return ErrAlreadyReversed
		}

		acc, err := r.lockAccount(tx, origin.CaseID, origin.ClientID)
		if err != nil {
			return err
		}
		// 账户锁后再保险复查一次幂等键。
		if existing, err := r.FindEntryByIdempotencyKey(tx, reversal.IdempotencyKey); err != nil {
			return err
		} else if existing != nil {
			result = &PostedResult{Entry: existing, Replayed: true, AccountID: existing.AccountID}
			return nil
		}

		delta := -origin.DeltaCents // 反向
		requireNonNeg := delta < 0  // 反向冲销「预收」时为负向，需要占用当前余额
		ok, err := r.applyDelta(tx, acc.ID, delta, requireNonNeg)
		if err != nil {
			return err
		}
		if !ok {
			if requireNonNeg {
				return ErrInsufficient
			}
			return ErrConflict
		}
		newBalance, err := r.reloadBalance(tx, acc.ID)
		if err != nil {
			return err
		}

		reversal.AccountID = acc.ID
		reversal.CaseID = origin.CaseID
		reversal.ClientID = origin.ClientID
		reversal.DeltaCents = delta
		reversal.ReversalOfID = origin.ID
		reversal.BalanceCents = newBalance
		if err := r.insertEntry(tx, reversal); err != nil {
			if errors.Is(err, ErrDuplicate) {
				return ErrAlreadyReversed
			}
			return err
		}

		linked, err := r.markReversalLink(tx, origin.ID, reversal.ID)
		if err != nil {
			return err
		}
		if !linked {
			// 已被其他事务冲销：回滚本次冲销与余额变动，避免重复冲销污染余额。
			return ErrAlreadyReversed
		}

		result = &PostedResult{Entry: reversal, AccountID: acc.ID}
		return nil
	})
	if err != nil {
		if replay, ok := r.replayIfPresent(reversal.IdempotencyKey); ok {
			return replay, nil
		}
		return nil, err
	}
	return result, nil
}

// ReconcileAccount 重算指定专案账户的权威余额（对 posted 明细求带符号和），
// 与物化余额比对，返回 (重算值, 物化值, 是否一致)。明细是唯一事实来源。
func (r *FundRepository) ReconcileAccount(caseID, clientID uint64) (computed int64, stored int64, consistent bool, err error) {
	acc, err := r.getAccountByCaseClient(caseID, clientID)
	if err != nil {
		return 0, 0, false, err
	}
	if err := r.db.Model(&model.FundEntry{}).
		Where("account_id = ?", acc.ID).
		Select("COALESCE(SUM(delta_cents),0)").Scan(&computed).Error; err != nil {
		return 0, 0, false, fmt.Errorf("recompute fund balance: %w", err)
	}
	return computed, acc.BalanceCents, computed == acc.BalanceCents, nil
}

func (r *FundRepository) getAccountByCaseClient(caseID, clientID uint64) (*model.FundAccount, error) {
	var acc model.FundAccount
	err := r.db.Where("case_id = ? AND client_id = ?", caseID, clientID).First(&acc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find fund account: %w", err)
	}
	return &acc, nil
}

// GetAccount 读取专案账户。
func (r *FundRepository) GetAccount(caseID, clientID uint64) (*model.FundAccount, error) {
	return r.getAccountByCaseClient(caseID, clientID)
}

// ListAccounts 分页列出专案账户。
func (r *FundRepository) ListAccounts(page, pageSize int) ([]model.FundAccount, int64, error) {
	var list []model.FundAccount
	var total int64
	q := r.db.Model(&model.FundAccount{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count fund accounts: %w", err)
	}
	if err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("list fund accounts: %w", err)
	}
	return list, total, nil
}

// ListEntries 分页查询明细，可按案件/客户/类型过滤。
func (r *FundRepository) ListEntries(page, pageSize int, caseID, clientID uint64, entryType string) ([]model.FundEntry, int64, error) {
	var list []model.FundEntry
	var total int64
	q := r.db.Model(&model.FundEntry{})
	if caseID > 0 {
		q = q.Where("case_id = ?", caseID)
	}
	if clientID > 0 {
		q = q.Where("client_id = ?", clientID)
	}
	if entryType != "" {
		q = q.Where("entry_type = ?", entryType)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count fund entries: %w", err)
	}
	if err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("list fund entries: %w", err)
	}
	return list, total, nil
}

// ListEntriesByCase 列出某案件的全部明细（按时间正序，便于台账/时间线展示）。
func (r *FundRepository) ListEntriesByCase(caseID uint64) ([]model.FundEntry, error) {
	var list []model.FundEntry
	if err := r.db.Where("case_id = ?", caseID).Order("id ASC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list fund entries by case: %w", err)
	}
	return list, nil
}

// IsDuplicateKeyErr 判断是否唯一约束冲突（Postgres 23505 / SQLite UNIQUE）。
func IsDuplicateKeyErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicated key")
}
