package dto

import "encoding/json"

// FundPrepaymentRequest 登记客户预收款。
// Amount 接受数字或「两位小数字符串」，统一在服务层按分精确解析，避免 JSON 浮点误差。
type FundPrepaymentRequest struct {
	CaseID         uint64      `json:"case_id" binding:"required"`
	ClientID       uint64      `json:"client_id" binding:"required"`
	Amount         json.Number `json:"amount" binding:"required"`
	Subject        string      `json:"subject" binding:"max=200"`
	Remark         string      `json:"remark" binding:"max=500"`
	IdempotencyKey string      `json:"idempotency_key" binding:"required,max=64"`
}

// FundExpenseRequest 登记办案支出，仅可使用该案专案可用余额。
type FundExpenseRequest struct {
	CaseID         uint64      `json:"case_id" binding:"required"`
	ClientID       uint64      `json:"client_id" binding:"required"`
	Amount         json.Number `json:"amount" binding:"required"`
	Subject        string      `json:"subject" binding:"max=200"`
	Remark         string      `json:"remark" binding:"max=500"`
	IdempotencyKey string      `json:"idempotency_key" binding:"required,max=64"`
}

// FundReverseRequest 反向冲销已入账明细；原记录不改不删，仅追加反向明细。
type FundReverseRequest struct {
	EntryID        uint64 `json:"entry_id" binding:"required"`
	Reason         string `json:"reason" binding:"max=500"`
	IdempotencyKey string `json:"idempotency_key" binding:"required,max=64"`
}

// FundEntryResponse 明细响应，金额字段统一输出两位小数字符串（元）。
type FundEntryResponse struct {
	ID             uint64 `json:"id"`
	EntryNo        string `json:"entry_no"`
	AccountID      uint64 `json:"account_id"`
	CaseID         uint64 `json:"case_id"`
	ClientID       uint64 `json:"client_id"`
	EntryType      string `json:"entry_type"`
	Amount         string `json:"amount"`  // 发生额绝对值（元）
	Delta          string `json:"delta"`   // 带符号发生额（元）
	Balance        string `json:"balance"` // 入账后余额（元）
	IdempotencyKey string `json:"idempotency_key"`
	ReversalOfID   uint64 `json:"reversal_of_id"`
	ReversedByID   uint64 `json:"reversed_by_id"`
	Reversed       bool   `json:"reversed"`
	Subject        string `json:"subject"`
	Remark         string `json:"remark"`
	OperatorID     uint64 `json:"operator_id"`
	OperatorName   string `json:"operator_name"`
	CreatedAt      string `json:"created_at"`
}

// FundAccountResponse 专案账户余额响应。
type FundAccountResponse struct {
	ID         uint64 `json:"id"`
	CaseID     uint64 `json:"case_id"`
	ClientID   uint64 `json:"client_id"`
	Balance    string `json:"balance"`
	Reconciled string `json:"reconciled_balance"`
	Consistent bool   `json:"consistent"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}
