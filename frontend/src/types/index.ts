export interface User {
  id: number
  username: string
  real_name: string
  role: string
  license_no: string
  email: string
  phone: string
  avatar: string
  created_at: string
}

export interface Client {
  id: number
  name: string
  id_number: string
  contact: string
  address: string
  remark: string
  created_at: string
}

export interface CaseItem {
  id: number
  case_no: string
  title: string
  case_type: string
  status: string
  accept_date: string | null
  close_date: string | null
  summary: string
  client_id: number
  lead_lawyer_id: number
  co_lawyer_ids: number[]
  created_at: string
}

export interface DocumentItem {
  id: number
  title: string
  file_type: string
  file_url: string
  upload_time: string
  case_id: number
  uploader_id: number
  created_at: string
}

export interface Billing {
  id: number
  bill_no: string
  billing_type: string
  amount: number | string
  status: string
  case_id: number
  client_id: number
  invoice_info: string
  created_at: string
}

// 资金往来明细：金额均为两位小数字符串（元），与后端 dto.FundEntryResponse 对齐。
export interface FundEntry {
  id: number
  entry_no: string
  account_id: number
  case_id: number
  client_id: number
  entry_type: string
  amount: string
  delta: string
  balance: string
  idempotency_key: string
  reversal_of_id: number
  reversed_by_id: number
  reversed: boolean
  subject: string
  remark: string
  operator_id: number
  operator_name: string
  created_at: string
}

export interface FundAccount {
  id: number
  case_id: number
  client_id: number
  balance: string
  reconciled_balance: string
  consistent: boolean
  updated_at: string
}

export interface AuditLog {
  id: number
  operator_id: number
  operator_name: string
  action: string
  entity_type: string
  entity_id: string
  detail: string
  ip: string
  created_at: string
}

export interface PageResult<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}
