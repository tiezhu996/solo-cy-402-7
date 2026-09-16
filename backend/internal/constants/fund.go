package constants

// FundEntryType 资金往来明细类型枚举。
const (
	FundEntryPrepayment = "prepayment" // 客户预收（入账，余额增加）
	FundEntryExpense    = "expense"    // 办案支出（出账，余额减少）
	FundEntryReversal   = "reversal"   // 反向冲销（方向与被冲销明细相反）
)

// FundEntryTypeValues 资金往来明细类型全集。
var FundEntryTypeValues = []string{FundEntryPrepayment, FundEntryExpense, FundEntryReversal}

// IsValidFundEntryType 校验明细类型。
func IsValidFundEntryType(s string) bool {
	for _, v := range FundEntryTypeValues {
		if v == s {
			return true
		}
	}
	return false
}

// FundEntryStatusPosted 明细入账状态：入账即终态，不可改、不可删。
const FundEntryStatusPosted = "posted"

// 资金台账单笔金额上限（分）：1 亿元，防止异常大额把余额顶出 int64。
const FundMaxAmountCents int64 = 100_0000_0000_00
