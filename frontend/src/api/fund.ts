import request from '@/utils/request'
import type { FundEntry, FundAccount } from '@/types'

// 生成幂等键：优先使用浏览器 crypto.randomUUID，保证重复提交/网络重试只入账一次。
export function newIdempotencyKey(): string {
  const c = globalThis.crypto as Crypto | undefined
  if (c && typeof c.randomUUID === 'function') return c.randomUUID()
  return `idem-${Date.now()}-${Math.random().toString(16).slice(2)}-${Math.random().toString(16).slice(2)}`
}

export interface FundPostBody {
  case_id: number
  client_id: number
  amount: string | number
  subject?: string
  remark?: string
  idempotency_key: string
}

export function listFundEntries(params: { page?: number; page_size?: number; case_id?: number; client_id?: number; entry_type?: string }) {
  return request.get('/fund/entries', { params })
}

export function listFundEntriesByCase(caseId: number) {
  return request.get(`/fund/entries/by-case/${caseId}`)
}

export function listFundAccounts(params: { page?: number; page_size?: number }) {
  return request.get('/fund/accounts', { params })
}

export function getFundBalance(params: { case_id: number; client_id?: number }) {
  return request.get('/fund/balance', { params })
}

export function createPrepayment(data: FundPostBody) {
  return request.post('/fund/prepayments', data)
}

export function createExpense(data: FundPostBody) {
  return request.post('/fund/expenses', data)
}

export function reverseFundEntry(data: { entry_id: number; reason: string; idempotency_key: string }): Promise<{
  code: number
  message: string
  data: { entry: FundEntry; replayed: boolean }
}> {
  return request.post('/fund/reversals', data)
}
