import { create } from 'zustand'
import { listFundEntries } from '@/api/fund'
import type { FundEntry } from '@/types'

interface FundState {
  list: FundEntry[]
  total: number
  fetchList: (params?: Record<string, unknown>) => Promise<void>
}

export const useFundStore = create<FundState>((set) => ({
  list: [],
  total: 0,
  async fetchList(params = {}) {
    const res: any = await listFundEntries(params)
    set({ list: res.data.list, total: res.data.total })
  },
}))
