// 资金往来明细类型（与后端 backend/internal/constants/fund.go 保持一致）
export const FundEntryType = {
  PREPAYMENT: 'prepayment',
  EXPENSE: 'expense',
  REVERSAL: 'reversal',
} as const

export const FundEntryTypeText: Record<string, string> = {
  [FundEntryType.PREPAYMENT]: '客户预收',
  [FundEntryType.EXPENSE]: '办案支出',
  [FundEntryType.REVERSAL]: '反向冲销',
}

export const FundEntryTypeOptions = Object.entries(FundEntryTypeText).map(([value, label]) => ({ label, value }))

// 冲销对象类型中文
export const reversalOf = (type: string) => {
  if (type === FundEntryType.PREPAYMENT) return '冲销预收'
  if (type === FundEntryType.EXPENSE) return '冲销支出'
  return '冲销'
}
