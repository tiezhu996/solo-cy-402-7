package model

import "time"

// FundAccount 专案资金账户：一个（案件 + 客户）对应唯一账户，余额单位为「分」。
// 余额是 fund_entries 的物化缓存；权威余额始终可由明细重算得到（见 ReconcileBalance）。
type FundAccount struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	CaseID       uint64    `gorm:"not null;uniqueIndex:uni_fund_account_case_client,priority:1" json:"case_id"`
	ClientID     uint64    `gorm:"not null;uniqueIndex:uni_fund_account_case_client,priority:2" json:"client_id"`
	BalanceCents int64     `gorm:"not null;default:0;check:balance_cents >= 0" json:"balance_cents"`
	Version      uint64    `gorm:"not null;default:0" json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (FundAccount) TableName() string { return "fund_accounts" }

// FundEntry 资金往来明细。明细为只追加（append-only）：
// 录错只能新增一笔 reversal 反向冲销，原记录不可改、不可删。
// DeltaCents 为带符号发生额：预收为正，支出为负，冲销方向与被冲销明细相反。
type FundEntry struct {
	ID             uint64 `gorm:"primaryKey" json:"id"`
	EntryNo        string `gorm:"size:40;uniqueIndex;not null;default:''" json:"entry_no"`
	AccountID      uint64 `gorm:"not null;index" json:"account_id"`
	CaseID         uint64 `gorm:"not null;index" json:"case_id"`
	ClientID       uint64 `gorm:"not null;index" json:"client_id"`
	EntryType      string `gorm:"size:20;not null;index" json:"entry_type"`
	DeltaCents     int64  `gorm:"not null" json:"delta_cents"`
	BalanceCents   int64  `gorm:"not null" json:"balance_cents"`
	IdempotencyKey string `gorm:"size:64;not null;uniqueIndex:uni_fund_entry_idem,priority:1" json:"idempotency_key"`
	// ReversalOfID 非 0 表示本明细是对某条已入账明细的反向冲销。
	// 「只能冲销一次」由部分唯一索引（WHERE reversal_of_id <> 0）与 reversed_by_id 乐观抢占共同保证。
	ReversalOfID uint64 `gorm:"not null;default:0;index" json:"reversal_of_id"`
	// ReversedByID 非 0 表示本明细已被该冲销明细反向，原明细仍保留不改。
	ReversedByID uint64    `gorm:"not null;default:0;index" json:"reversed_by_id"`
	Subject      string    `gorm:"size:200;not null;default:''" json:"subject"`
	Remark       string    `gorm:"size:500;not null;default:''" json:"remark"`
	OperatorID   uint64    `gorm:"not null;default:0" json:"operator_id"`
	OperatorName string    `gorm:"size:50;not null;default:''" json:"operator_name"`
	CreatedAt    time.Time `gorm:"not null;index" json:"created_at"`
}

// TableName 指定表名。
func (FundEntry) TableName() string { return "fund_entries" }

// IsReversal 是否为冲销明细。
func (e *FundEntry) IsReversal() bool {
	return e.EntryType == "reversal" && e.ReversalOfID > 0
}
