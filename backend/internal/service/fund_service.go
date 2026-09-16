package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"cylawcase/internal/constants"
	"cylawcase/internal/model"
	"cylawcase/internal/repository"
	"cylawcase/internal/util"
)

// FundOperator 记录入账操作人（由 handler 从 JWT 上下文注入）。
type FundOperator struct {
	ID   uint64
	Name string
}

// FundService 案件资金往来台账业务逻辑。
type FundService struct {
	repo       *repository.FundRepository
	caseRepo   *repository.CaseRepository
	clientRepo *repository.ClientRepository
	logger     *slog.Logger
}

// NewFundService 构造资金台账服务。
func NewFundService(repo *repository.FundRepository, caseRepo *repository.CaseRepository,
	clientRepo *repository.ClientRepository, logger *slog.Logger) *FundService {
	return &FundService{repo: repo, caseRepo: caseRepo, clientRepo: clientRepo, logger: logger}
}

// validateCaseClient 校验案件、客户均存在，且该笔资金必须归属同一案件与同一客户。
func (s *FundService) validateCaseClient(caseID, clientID uint64) error {
	cs, err := s.caseRepo.FindByID(caseID)
	if err != nil {
		return util.Wrap(err, "Fund[case_id=%d] validate: case not found", caseID)
	}
	if _, err := s.clientRepo.FindByID(clientID); err != nil {
		return util.Wrap(err, "Fund[client_id=%d] validate: client not found", clientID)
	}
	if cs.ClientID != clientID {
		return util.NewAppError(constants.CodeValidationFailed,
			fmt.Sprintf("Fund[case_id=%d client_id=%d] validate: entry must belong to the case and its own client(case.client_id=%d)",
				caseID, clientID, cs.ClientID))
	}
	return nil
}

func (s *FundService) validateAmount(cents int64, action string) error {
	if cents <= 0 {
		return util.NewAppError(constants.CodeValidationFailed,
			fmt.Sprintf("Fund %s: amount must be positive, got %d cents", action, cents))
	}
	if cents > constants.FundMaxAmountCents {
		return util.NewAppError(constants.CodeValidationFailed,
			fmt.Sprintf("Fund %s: amount exceeds limit %d cents", action, constants.FundMaxAmountCents))
	}
	return nil
}

// nextEntryNo 生成全局唯一的资金流水号：前缀 + 纳秒时间戳 + 随机后缀。
// 必须含随机分量，否则多个服务实例在同一纳秒会生成相同流水号，触发唯一冲突导致整笔失败。
func (s *FundService) nextEntryNo(prefix string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败极罕见；退回时间戳拼接仍可保证进程内不重复。
		return fmt.Sprintf("%s%dX", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s%d%s", prefix, time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

// RegisterPrepayment 登记客户预收款：同一案件+客户，余额增加。幂等键保证重复提交/并发重试仅入账一次。
func (s *FundService) RegisterPrepayment(caseID, clientID uint64, cents int64, subject, remark, idem string, op FundOperator) (*model.FundEntry, bool, error) {
	if err := s.validateAmount(cents, "prepayment"); err != nil {
		return nil, false, err
	}
	if err := s.validateCaseClient(caseID, clientID); err != nil {
		return nil, false, err
	}
	entry := &model.FundEntry{
		EntryNo:        s.nextEntryNo("PRE"),
		CaseID:         caseID,
		ClientID:       clientID,
		EntryType:      constants.FundEntryPrepayment,
		DeltaCents:     cents,
		IdempotencyKey: idem,
		Subject:        subject,
		Remark:         remark,
		OperatorID:     op.ID,
		OperatorName:   op.Name,
		CreatedAt:      time.Now(),
	}
	res, err := s.repo.PostPrepayment(entry)
	if err != nil {
		s.logger.Error(constants.LogFundPrepaymentFailed, "case_id", caseID, "client_id", clientID, "error", err.Error())
		return nil, false, s.mapRepoError(err, "prepayment")
	}
	if res.Replayed {
		s.logger.Info(constants.LogFundIdempotentReplay, "idempotency_key", idem, "entry_id", res.Entry.ID, "type", "prepayment")
	} else {
		s.logger.Info(constants.LogFundPrepaymentSuccess, "entry_id", res.Entry.ID, "case_id", caseID, "cents", cents)
	}
	return res.Entry, res.Replayed, nil
}

// RegisterExpense 登记办案支出：仅可使用该案专案可用余额。
// 余额不足时仓储层整笔回滚（ErrInsufficient），原明细与余额保持不变。
func (s *FundService) RegisterExpense(caseID, clientID uint64, cents int64, subject, remark, idem string, op FundOperator) (*model.FundEntry, bool, error) {
	if err := s.validateAmount(cents, "expense"); err != nil {
		return nil, false, err
	}
	if err := s.validateCaseClient(caseID, clientID); err != nil {
		return nil, false, err
	}
	entry := &model.FundEntry{
		EntryNo:        s.nextEntryNo("EXP"),
		CaseID:         caseID,
		ClientID:       clientID,
		EntryType:      constants.FundEntryExpense,
		DeltaCents:     -cents,
		IdempotencyKey: idem,
		Subject:        subject,
		Remark:         remark,
		OperatorID:     op.ID,
		OperatorName:   op.Name,
		CreatedAt:      time.Now(),
	}
	res, err := s.repo.PostExpense(entry)
	if err != nil {
		if errors.Is(err, repository.ErrInsufficient) {
			s.logger.Warn(constants.LogFundExpenseDenied, "case_id", caseID, "client_id", clientID, "cents", cents)
			return nil, false, util.NewAppError(constants.CodeFundInsufficient,
				fmt.Sprintf("Fund[case_id=%d client_id=%d] expense %d cents denied: %s",
					caseID, clientID, cents, constants.MsgFundInsufficient))
		}
		s.logger.Error(constants.LogFundExpenseFailed, "case_id", caseID, "client_id", clientID, "error", err.Error())
		return nil, false, s.mapRepoError(err, "expense")
	}
	if res.Replayed {
		s.logger.Info(constants.LogFundIdempotentReplay, "idempotency_key", idem, "entry_id", res.Entry.ID, "type", "expense")
	} else {
		s.logger.Info(constants.LogFundExpenseSuccess, "entry_id", res.Entry.ID, "case_id", caseID, "cents", cents)
	}
	return res.Entry, res.Replayed, nil
}

// ReverseEntry 录错纠错：仅允许对已入账明细做反向冲销。
// 原明细不可改、不可删；并发重复冲销只有一笔成功，另一笔返回冲突，绝不重复冲销污染余额。
func (s *FundService) ReverseEntry(originID uint64, reason, idem string, op FundOperator) (*model.FundEntry, bool, error) {
	if reason == "" {
		return nil, false, util.NewAppError(constants.CodeValidationFailed, "Fund reversal: reason is required")
	}
	reversal := &model.FundEntry{
		EntryNo:        s.nextEntryNo("REV"),
		EntryType:      constants.FundEntryReversal,
		IdempotencyKey: idem,
		Subject:        fmt.Sprintf("冲销明细 #%d", originID),
		Remark:         reason,
		OperatorID:     op.ID,
		OperatorName:   op.Name,
		CreatedAt:      time.Now(),
	}
	res, err := s.repo.PostReversal(reversal, originID)
	if err != nil {
		s.logger.Error(constants.LogFundReverseFailed, "origin_id", originID, "error", err.Error())
		return nil, false, s.mapReverseError(err, originID)
	}
	if res.Replayed {
		s.logger.Info(constants.LogFundIdempotentReplay, "idempotency_key", idem, "entry_id", res.Entry.ID, "type", "reversal")
	} else {
		s.logger.Info(constants.LogFundReverseSuccess, "reversal_id", res.Entry.ID, "origin_id", originID)
	}
	return res.Entry, res.Replayed, nil
}

func (s *FundService) mapReverseError(err error, originID uint64) error {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return util.NewAppError(constants.CodeNotFound,
			fmt.Sprintf("Fund[entry_id=%d] reverse: %s", originID, constants.MsgFundEntryNotFound))
	case errors.Is(err, repository.ErrAlreadyReversed):
		return util.NewAppError(constants.CodeFundAlreadyReversed,
			fmt.Sprintf("Fund[entry_id=%d] reverse: %s", originID, constants.MsgFundAlreadyReversed))
	case errors.Is(err, repository.ErrCannotReverseReversal):
		return util.NewAppError(constants.CodeValidationFailed,
			fmt.Sprintf("Fund[entry_id=%d] reverse: cannot reverse a reversal entry", originID))
	case errors.Is(err, repository.ErrInsufficient):
		return util.NewAppError(constants.CodeFundInsufficient,
			fmt.Sprintf("Fund[entry_id=%d] reverse: %s", originID, constants.MsgFundInsufficient))
	case errors.Is(err, repository.ErrIdempotencyMismatch):
		return util.NewAppError(constants.CodeFundIdempotencyMismatch,
			fmt.Sprintf("Fund[entry_id=%d] reverse: %s", originID, constants.MsgFundIdempotencyMismatch))
	default:
		return util.Wrap(err, "Fund[entry_id=%d] reverse failed", originID)
	}
}

func (s *FundService) mapRepoError(err error, action string) error {
	switch {
	case errors.Is(err, repository.ErrDuplicate):
		return util.NewAppError(constants.CodeConflict, fmt.Sprintf("Fund %s: duplicate request", action))
	case errors.Is(err, repository.ErrConflict):
		return util.NewAppError(constants.CodeFundConflict, fmt.Sprintf("Fund %s: concurrent conflict, please retry", action))
	case errors.Is(err, repository.ErrIdempotencyMismatch):
		return util.NewAppError(constants.CodeFundIdempotencyMismatch,
			fmt.Sprintf("Fund %s: %s", action, constants.MsgFundIdempotencyMismatch))
	default:
		return err
	}
}

// Balance 读取专案账户，并以明细重算权威余额，返回两者是否一致。
func (s *FundService) Balance(caseID, clientID uint64) (computed, stored int64, consistent bool, err error) {
	computed, stored, consistent, err = s.repo.ReconcileAccount(caseID, clientID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// 尚无账户：权威余额与物化余额都为 0，天然一致。
			return 0, 0, true, nil
		}
		return 0, 0, false, util.Wrap(err, "Fund[case_id=%d client_id=%d] balance failed", caseID, clientID)
	}
	if !consistent {
		s.logger.Error(constants.LogFundReconcileMismatch, "case_id", caseID, "client_id", clientID,
			"stored", stored, "recomputed", computed)
	}
	return computed, stored, consistent, nil
}

// ListEntries 分页查询明细。
func (s *FundService) ListEntries(page, pageSize int, caseID, clientID uint64, entryType string) ([]model.FundEntry, int64, error) {
	if entryType != "" && !constants.IsValidFundEntryType(entryType) {
		return nil, 0, util.NewAppError(constants.CodeValidationFailed, "Fund list: invalid entry_type="+entryType)
	}
	return s.repo.ListEntries(page, pageSize, caseID, clientID, entryType)
}

// ListEntriesByCase 某案件全部明细。
func (s *FundService) ListEntriesByCase(caseID uint64) ([]model.FundEntry, error) {
	return s.repo.ListEntriesByCase(caseID)
}

// ListAccounts 分页列出专案账户（附按明细重算的权威余额与一致性标记）。
func (s *FundService) ListAccounts(page, pageSize int) ([]AccountBalanceView, int64, error) {
	accounts, total, err := s.repo.ListAccounts(page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	views := make([]AccountBalanceView, 0, len(accounts))
	for i := range accounts {
		acc := accounts[i]
		var computed int64
		if err := s.sumEntries(acc.ID, &computed); err != nil {
			return nil, 0, err
		}
		if computed != acc.BalanceCents {
			s.logger.Error(constants.LogFundReconcileMismatch, "account_id", acc.ID,
				"stored", acc.BalanceCents, "recomputed", computed)
		}
		views = append(views, AccountBalanceView{Account: acc, RecomputedCents: computed, Consistent: computed == acc.BalanceCents})
	}
	return views, total, nil
}

func (s *FundService) sumEntries(accountID uint64, out *int64) error {
	return s.repo.DB().Model(&model.FundEntry{}).
		Where("account_id = ?", accountID).
		Select("COALESCE(SUM(delta_cents),0)").Scan(out).Error
}

// AccountBalanceView 账户 + 重算余额视图。
type AccountBalanceView struct {
	Account         model.FundAccount
	RecomputedCents int64
	Consistent      bool
}
