import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Button, Card, Col, Form, Input, InputNumber, Modal, Row, Select, Space, Statistic, Table, Tag, Tooltip, message,
} from 'antd'
import { PlusOutlined, MinusOutlined, UndoOutlined, ReloadOutlined } from '@ant-design/icons'
import { useFundStore } from '@/stores/fundStore'
import { listCases } from '@/api/case'
import { listClients } from '@/api/client'
import {
  createPrepayment, createExpense, reverseFundEntry, getFundBalance, newIdempotencyKey,
} from '@/api/fund'
import { FundEntryTypeOptions, FundEntryTypeText } from '@/constants/fund'
import { formatAmount } from '@/utils/amountFormatter'
import dayjs from 'dayjs'
import type { CaseItem, Client, FundEntry } from '@/types'

type Mode = 'prepayment' | 'expense'

// 金额以两位小数字符串提交，避免 JSON 浮点误差（后端按分精确解析）。
function amountString(v: number | string | null): string {
  if (v === null || v === undefined || v === '') return ''
  const s = String(v)
  return /^\d+(\.\d{1,2})?$/.test(s) ? s : ''
}

export default function FundLedger() {
  const store = useFundStore()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [filters, setFilters] = useState<Record<string, unknown>>({})

  const [cases, setCases] = useState<CaseItem[]>([])
  const [clients, setClients] = useState<Client[]>([])
  const [caseFilter, setCaseFilter] = useState<number | undefined>()
  const [typeFilter, setTypeFilter] = useState<string | undefined>()

  const [balance, setBalance] = useState<{ balance: string; reconciled_balance: string; consistent: boolean } | null>(null)

  const [postOpen, setPostOpen] = useState(false)
  const [mode, setMode] = useState<Mode>('prepayment')
  const [submitting, setSubmitting] = useState(false)
  const [postForm] = Form.useForm()

  const [reverseTarget, setReverseTarget] = useState<FundEntry | null>(null)
  const [reverseOpen, setReverseOpen] = useState(false)
  const [reason, setReason] = useState('')

  const clientMap = useMemo(() => new Map(clients.map((c) => [c.id, c.name])), [clients])

  const refresh = useCallback(() => {
    store.fetchList({ page, page_size: pageSize, ...filters })
  }, [store, page, pageSize, filters])

  useEffect(() => { refresh() }, [refresh])

  useEffect(() => {
    listCases({ page: 1, page_size: 200 }).then((res: any) => setCases(res.data.list || []))
    listClients({ page: 1, page_size: 200 }).then((res: any) => setClients(res.data.list || []))
  }, [])

  const refreshBalance = useCallback((caseId?: number, clientId?: number) => {
    if (!caseId) { setBalance(null); return }
    getFundBalance({ case_id: caseId, client_id: clientId }).then((res: any) => setBalance(res.data)).catch(() => setBalance(null))
  }, [])

  useEffect(() => {
    const cid = cases.find((c) => c.id === caseFilter)?.client_id
    refreshBalance(caseFilter, cid)
  }, [caseFilter, cases, store.list, refreshBalance])

  function openPost(m: Mode) {
    setMode(m)
    postForm.resetFields()
    if (caseFilter) {
      const cs = cases.find((c) => c.id === caseFilter)
      postForm.setFieldsValue({ case_id: caseFilter, client_id: cs?.client_id })
    }
    setPostOpen(true)
  }

  async function onPost() {
    const values = await postForm.validateFields()
    const amount = amountString(values.amount)
    if (!amount) { message.error('金额必须为不超过两位小数的非负数'); return }
    const body = {
      case_id: values.case_id,
      client_id: values.client_id,
      amount,
      subject: values.subject || '',
      remark: values.remark || '',
      idempotency_key: newIdempotencyKey(),
    }
    setSubmitting(true)
    try {
      const res: any = mode === 'prepayment' ? await createPrepayment(body) : await createExpense(body)
      if (res.data?.replayed) message.warning('该笔为重复请求，已幂等返回首次入账结果，未重复入账')
      else message.success(res.message || (mode === 'prepayment' ? '预收已入账' : '支出已入账'))
      setPostOpen(false)
      postForm.resetFields()
      setCaseFilter(values.case_id)
      setPage(1)
      refresh()
    } catch {
      /* 拦截器已提示 */
    } finally {
      setSubmitting(false)
    }
  }

  function openReverse(row: FundEntry) {
    setReverseTarget(row)
    setReason('')
    setReverseOpen(true)
  }

  async function onReverse() {
    if (!reverseTarget) return
    if (!reason.trim()) { message.error('请填写冲销原因'); return }
    setSubmitting(true)
    try {
      const res: any = await reverseFundEntry({
        entry_id: reverseTarget.id,
        reason: reason.trim(),
        idempotency_key: newIdempotencyKey(),
      })
      if (res.data?.replayed) message.warning('该冲销为重复请求，已幂等返回首次结果')
      else message.success('已反向冲销，原明细保持不变')
      setReverseOpen(false)
      refresh()
    } catch {
      /* 拦截器已提示 */
    } finally {
      setSubmitting(false)
    }
  }

  function onCaseChange(caseId?: number) {
    const cs = cases.find((c) => c.id === caseId)
    postForm.setFieldsValue({ client_id: cs?.client_id })
  }

  const columns = [
    { title: '流水号', dataIndex: 'entry_no', width: 170, ellipsis: true },
    {
      title: '类型', dataIndex: 'entry_type', width: 110,
      render: (v: string, row: FundEntry) => {
        const color = v === 'prepayment' ? 'green' : v === 'expense' ? 'red' : 'orange'
        const text = v === 'reversal'
          ? (row.reversal_of_id ? `${FundEntryTypeText[v]} #${row.reversal_of_id}` : FundEntryTypeText[v])
          : FundEntryTypeText[v]
        return <Tag color={color}>{text}</Tag>
      },
    },
    { title: '案件', dataIndex: 'case_id', width: 80, render: (v: number) => `#${v}` },
    {
      title: '客户', dataIndex: 'client_id', width: 160, ellipsis: true,
      render: (v: number) => clientMap.get(v) || `客户#${v}`,
    },
    { title: '事由', dataIndex: 'subject', ellipsis: true },
    {
      title: '发生额', dataIndex: 'delta', width: 130, align: 'right' as const,
      render: (v: string) => {
        const neg = v.trim().startsWith('-')
        return <span style={{ color: neg ? '#cf1322' : '#3f8600', fontWeight: 600 }}>{neg ? '-' : '+'}{formatAmount(v.replace('-', ''))}</span>
      },
    },
    {
      title: '入账后余额', dataIndex: 'balance', width: 130, align: 'right' as const,
      render: (v: string) => formatAmount(v),
    },
    { title: '经办人', dataIndex: 'operator_name', width: 100 },
    { title: '时间', dataIndex: 'created_at', width: 170, render: (v: string) => dayjs(v).format('YYYY-MM-DD HH:mm:ss') },
    {
      title: '操作', width: 100,
      render: (_: unknown, row: FundEntry) => (
        <Tooltip title={row.reversed ? '该明细已冲销，不能重复冲销' : row.entry_type === 'reversal' ? '冲销明细不可再冲销' : '新增一笔反向冲销，原明细不改不删'}>
          <Button
            size="small" danger icon={<UndoOutlined />}
            disabled={row.reversed || row.entry_type === 'reversal'}
            onClick={() => openReverse(row)}
          >冲销</Button>
        </Tooltip>
      ),
    },
  ]

  return (
    <Card
      title="案件资金往来台账"
      extra={
        <Space>
          <Button icon={<ReloadOutlined />} onClick={refresh}>刷新</Button>
          <Button icon={<PlusOutlined />} onClick={() => openPost('prepayment')}>登记预收</Button>
          <Button danger icon={<MinusOutlined />} onClick={() => openPost('expense')}>登记支出</Button>
        </Space>
      }
    >
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={8}>
          <Statistic
            title={caseFilter ? `专案可用余额（案件 #${caseFilter}，单位：元）` : '专案可用余额（请先选择案件，单位：元）'}
            value={balance ? balance.balance : '—'}
            precision={2}
            valueStyle={{ color: '#1677ff' }}
          />
        </Col>
        <Col span={8}>
          <Statistic
            title="按明细重算余额（权威，单位：元）"
            value={balance ? balance.reconciled_balance : '—'}
            precision={2}
          />
        </Col>
        <Col span={8}>
          <div style={{ color: 'rgba(0,0,0,0.45)', fontSize: 14, marginBottom: 8 }}>余额一致性</div>
          {balance
            ? (balance.consistent ? <Tag color="green" style={{ fontSize: 16, padding: '4px 12px' }}>一致</Tag>
              : <Tag color="red" style={{ fontSize: 16, padding: '4px 12px' }}>不一致</Tag>)
            : <Tag style={{ fontSize: 16, padding: '4px 12px' }}>未选择案件</Tag>}
        </Col>
      </Row>

      <Space style={{ marginBottom: 16 }} wrap>
        <Select
          showSearch allowClear placeholder="按案件筛选" style={{ width: 260 }}
          optionFilterProp="label"
          options={cases.map((c) => ({ value: c.id, label: `#${c.id} ${c.case_no} ${c.title}` }))}
          value={caseFilter}
          onChange={(v) => { setCaseFilter(v); setFilters((f) => ({ ...f, case_id: v || undefined })); setPage(1) }}
        />
        <Select
          allowClear placeholder="明细类型" style={{ width: 140 }}
          options={FundEntryTypeOptions}
          value={typeFilter}
          onChange={(v) => { setTypeFilter(v); setFilters((f) => ({ ...f, entry_type: v || undefined })); setPage(1) }}
        />
      </Space>

      <Table<FundEntry>
        rowKey="id"
        dataSource={store.list}
        columns={columns}
        scroll={{ x: 1280 }}
        pagination={{
          current: page, pageSize, total: store.total, showSizeChanger: true,
          onChange: (p, ps) => { setPage(p); setPageSize(ps) },
        }}
      />

      <Modal
        title={mode === 'prepayment' ? '登记客户预收款' : '登记办案支出'}
        open={postOpen}
        onOk={onPost}
        confirmLoading={submitting}
        onCancel={() => setPostOpen(false)}
        okText={mode === 'prepayment' ? '入账' : '提交支出'}
        destroyOnClose
      >
        <Form form={postForm} layout="vertical">
          <Form.Item name="case_id" label="归属案件" rules={[{ required: true, message: '请选择案件' }]}>
            <Select
              showSearch optionFilterProp="label" placeholder="选择案件"
              options={cases.map((c) => ({ value: c.id, label: `#${c.id} ${c.case_no} ${c.title}` }))}
              onChange={onCaseChange}
            />
          </Form.Item>
          <Form.Item name="client_id" label="归属客户（自动取案件所属客户）" rules={[{ required: true, message: '客户必填' }]}>
            <Select
              showSearch optionFilterProp="label" placeholder="客户须与案件一致"
              options={clients.map((c) => ({ value: c.id, label: `#${c.id} ${c.name}` }))}
            />
          </Form.Item>
          <Form.Item name="amount" label="金额（元）" rules={[{ required: true, message: '请输入金额' }]}>
            <InputNumber style={{ width: '100%' }} min={0} precision={2} step={0.01} controls={false} />
          </Form.Item>
          <Form.Item name="subject" label="事由"><Input maxLength={200} placeholder="如：财产保全申请费" /></Form.Item>
          <Form.Item name="remark" label="备注"><Input.TextArea rows={2} maxLength={500} showCount /></Form.Item>
          {mode === 'expense' && <div style={{ color: '#cf1322', marginBottom: 8 }}>仅可使用该案专案可用余额，余额不足时整笔拒绝，不产生任何明细。</div>}
        </Form>
      </Modal>

      <Modal
        title="反向冲销"
        open={reverseOpen}
        onOk={onReverse}
        confirmLoading={submitting}
        onCancel={() => setReverseOpen(false)}
        okText="确认冲销"
        okButtonProps={{ danger: true }}
        destroyOnClose
      >
        {reverseTarget && (
          <Space direction="vertical" style={{ width: '100%' }}>
            <div>流水号：{reverseTarget.entry_no}</div>
            <div>类型：{FundEntryTypeText[reverseTarget.entry_type]}</div>
            <div>原发生额：
              <b style={{ color: reverseTarget.delta.startsWith('-') ? '#cf1322' : '#3f8600' }}>
                {reverseTarget.delta.startsWith('-') ? '-' : '+'}{formatAmount(reverseTarget.delta.replace('-', ''))}
              </b>
              ，冲销将记一笔反向金额 {reverseTarget.delta.startsWith('-') ? '+' : '-'}
              {formatAmount(reverseTarget.amount)} 元
            </div>
            <div style={{ color: '#888' }}>原明细不会被修改或删除，仅追加一条「反向冲销」明细。</div>
            <Input.TextArea
              rows={3} maxLength={500} showCount placeholder="请填写冲销原因（必填）"
              value={reason} onChange={(e) => setReason(e.target.value)}
            />
          </Space>
        )}
      </Modal>
    </Card>
  )
}
