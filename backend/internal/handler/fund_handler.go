package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"cylawcase/internal/constants"
	"cylawcase/internal/dto"
	"cylawcase/internal/middleware"
	"cylawcase/internal/model"
	"cylawcase/internal/service"
	"cylawcase/internal/util"

	"github.com/gin-gonic/gin"
)

// FundHandler 案件资金往来台账 HTTP 处理器。
type FundHandler struct {
	svc    *service.FundService
	logger *slog.Logger
}

// NewFundHandler 构造资金台账处理器。
func NewFundHandler(svc *service.FundService, logger *slog.Logger) *FundHandler {
	return &FundHandler{svc: svc, logger: logger}
}

// toEntryResponse 将明细模型转为响应（金额统一两位小数字符串）。
func toEntryResponse(e *model.FundEntry) dto.FundEntryResponse {
	amount := e.DeltaCents
	if amount < 0 {
		amount = -amount
	}
	return dto.FundEntryResponse{
		ID:             e.ID,
		EntryNo:        e.EntryNo,
		AccountID:      e.AccountID,
		CaseID:         e.CaseID,
		ClientID:       e.ClientID,
		EntryType:      e.EntryType,
		Amount:         util.FormatCents(amount),
		Delta:          util.FormatCents(e.DeltaCents),
		Balance:        util.FormatCents(e.BalanceCents),
		IdempotencyKey: e.IdempotencyKey,
		ReversalOfID:   e.ReversalOfID,
		ReversedByID:   e.ReversedByID,
		Reversed:       e.ReversedByID > 0,
		Subject:        e.Subject,
		Remark:         e.Remark,
		OperatorID:     e.OperatorID,
		OperatorName:   e.OperatorName,
		CreatedAt:      e.CreatedAt.Format(time.RFC3339),
	}
}

func toEntryList(list []model.FundEntry) []dto.FundEntryResponse {
	out := make([]dto.FundEntryResponse, 0, len(list))
	for i := range list {
		out = append(out, toEntryResponse(&list[i]))
	}
	return out
}

func operator(c *gin.Context) service.FundOperator {
	return service.FundOperator{ID: middleware.GetUserID(c), Name: middleware.GetUsername(c)}
}

// Prepayment 登记客户预收款。
func (h *FundHandler) Prepayment(c *gin.Context) {
	var req dto.FundPrepaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "Fund prepayment: "+err.Error())
		return
	}
	cents, err := util.ParseYuanToCents(req.Amount)
	if err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "Fund prepayment: invalid amount (use number with at most 2 decimals)")
		return
	}
	entry, replayed, err := h.svc.RegisterPrepayment(req.CaseID, req.ClientID, cents, req.Subject, req.Remark, req.IdempotencyKey, operator(c))
	if err != nil {
		h.wrapError(c, err, "Fund[case_id="+strconv.FormatUint(req.CaseID, 10)+"] prepayment failed")
		return
	}
	h.audit(c, "prepayment", entry.ID, replayed)
	msg := constants.MsgFundPrepaymentCreated
	if replayed {
		msg = "重复请求已幂等忽略，返回首次入账结果"
	}
	c.JSON(http.StatusOK, gin.H{"code": constants.CodeOK, "message": msg,
		"data": gin.H{"entry": toEntryResponse(entry), "replayed": replayed}})
}

// Expense 登记办案支出（余额不足整笔拒绝）。
func (h *FundHandler) Expense(c *gin.Context) {
	var req dto.FundExpenseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "Fund expense: "+err.Error())
		return
	}
	cents, err := util.ParseYuanToCents(req.Amount)
	if err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "Fund expense: invalid amount (use number with at most 2 decimals)")
		return
	}
	entry, replayed, err := h.svc.RegisterExpense(req.CaseID, req.ClientID, cents, req.Subject, req.Remark, req.IdempotencyKey, operator(c))
	if err != nil {
		h.wrapError(c, err, "Fund[case_id="+strconv.FormatUint(req.CaseID, 10)+"] expense failed")
		return
	}
	h.audit(c, "expense", entry.ID, replayed)
	msg := constants.MsgFundExpenseCreated
	if replayed {
		msg = "重复请求已幂等忽略，返回首次入账结果"
	}
	c.JSON(http.StatusOK, gin.H{"code": constants.CodeOK, "message": msg,
		"data": gin.H{"entry": toEntryResponse(entry), "replayed": replayed}})
}

// Reverse 反向冲销已入账明细。
func (h *FundHandler) Reverse(c *gin.Context) {
	var req dto.FundReverseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "Fund reverse: "+err.Error())
		return
	}
	entry, replayed, err := h.svc.ReverseEntry(req.EntryID, req.Reason, req.IdempotencyKey, operator(c))
	if err != nil {
		h.wrapError(c, err, "Fund[entry_id="+strconv.FormatUint(req.EntryID, 10)+"] reverse failed")
		return
	}
	h.audit(c, "reversal", entry.ID, replayed)
	msg := constants.MsgFundReversed
	if replayed {
		msg = "重复请求已幂等忽略，返回首次冲销结果"
	}
	c.JSON(http.StatusOK, gin.H{"code": constants.CodeOK, "message": msg,
		"data": gin.H{"entry": toEntryResponse(entry), "replayed": replayed}})
}

// Balance 查询某案件+客户专案余额（含重算一致性）。
func (h *FundHandler) Balance(c *gin.Context) {
	caseID, err := strconv.ParseUint(c.Query("case_id"), 10, 64)
	clientID, _ := strconv.ParseUint(c.Query("client_id"), 10, 64)
	if err != nil || caseID == 0 {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "Fund balance: case_id is required")
		return
	}
	computed, stored, consistent, err := h.svc.Balance(caseID, clientID)
	if err != nil {
		h.wrapError(c, err, "Fund balance failed")
		return
	}
	OK(c, gin.H{
		"case_id":            caseID,
		"client_id":          clientID,
		"balance":            util.FormatCents(stored),
		"reconciled_balance": util.FormatCents(computed),
		"consistent":         consistent,
	})
}

// List 明细分页列表。
func (h *FundHandler) List(c *gin.Context) {
	var q dto.PageQuery
	_ = c.ShouldBindQuery(&q)
	q.Normalize()
	caseID, _ := strconv.ParseUint(c.Query("case_id"), 10, 64)
	clientID, _ := strconv.ParseUint(c.Query("client_id"), 10, 64)
	list, total, err := h.svc.ListEntries(q.Page, q.PageSize, caseID, clientID, c.Query("entry_type"))
	if err != nil {
		h.wrapError(c, err, "Fund entry list failed")
		return
	}
	OK(c, pageResponse(toEntryList(list), total, q.Page, q.PageSize))
}

// ListByCase 某案件全部明细。
func (h *FundHandler) ListByCase(c *gin.Context) {
	caseID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "Fund list by case: invalid case id")
		return
	}
	list, err := h.svc.ListEntriesByCase(caseID)
	if err != nil {
		h.wrapError(c, err, "Fund list by case failed")
		return
	}
	OK(c, toEntryList(list))
}

// ListAccounts 专案账户余额分页。
func (h *FundHandler) ListAccounts(c *gin.Context) {
	var q dto.PageQuery
	_ = c.ShouldBindQuery(&q)
	q.Normalize()
	views, total, err := h.svc.ListAccounts(q.Page, q.PageSize)
	if err != nil {
		h.wrapError(c, err, "Fund account list failed")
		return
	}
	items := make([]gin.H, 0, len(views))
	for _, v := range views {
		items = append(items, gin.H{
			"id":                 v.Account.ID,
			"case_id":            v.Account.CaseID,
			"client_id":          v.Account.ClientID,
			"balance":            util.FormatCents(v.Account.BalanceCents),
			"reconciled_balance": util.FormatCents(v.RecomputedCents),
			"consistent":         v.Consistent,
			"updated_at":         v.Account.UpdatedAt.Format(time.RFC3339),
		})
	}
	OK(c, pageResponse(items, total, q.Page, q.PageSize))
}

func (h *FundHandler) audit(c *gin.Context, kind string, entryID uint64, replayed bool) {
	c.Set("audit_detail", map[string]any{"kind": kind, "entry_id": entryID, "replayed": replayed})
}

func (h *FundHandler) wrapError(c *gin.Context, err error, ctx string) {
	var appErr *util.AppError
	if errors.As(err, &appErr) {
		c.Set("audit_detail", appErr.Message)
		h.logger.Warn("fund handler error", "context", ctx, "error", appErr.Error())
		Fail(c, appErrorStatus(appErr.Code), appErr.Code, appErr.Message)
		return
	}
	h.logger.Error("fund handler error", "context", ctx, "error", err.Error())
	Fail(c, http.StatusInternalServerError, constants.CodeInternalError, constants.MsgInternalError)
}
