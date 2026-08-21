import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDownToLine, Banknote, Coins, FileText, Gift, HandCoins, ReceiptText, RotateCcw, WalletCards } from 'lucide-react'
import { api, apiDownload, asPage, formatDate, formatMoney } from '../lib/api'
import { useProjectScope } from '../lib/project-scope'
import { Button, DataTable, EmptyState, ErrorState, Form, Metric, Modal, Panel, Segmented, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type BillingTab = 'recharges' | 'usage' | 'subscriptions' | 'statements' | 'refunds' | 'invoices'
type ExportKind = 'recharges' | 'usage' | 'subscriptions' | 'monthly_statement'

type BalanceComposition = {
  wallet_id: string
  organization_id: string
  currency: string
  cash_available: string
  bonus_available: string
  credit_limit: string
  credit_used: string
  credit_available: string
  reserved_balance: string
  aggregate_balance: string
  refundable_cash: string
  attribution_gap: string
  as_of: string
}

type RechargeOrder = {
  id: string
  platform_order_no: string
  provider_order_no?: string
  payment_provider: string
  status: string
  amount: string
  currency: string
  wallet_transaction_id?: string
  ledger_journal_id?: string
  paid_at?: string
  credited_at?: string
  created_at: string
}

type FinanceUsage = {
  request_id: string
  project_id: string
  provider_id: string
  provider_name: string
  model: string
  input_tokens: number
  cached_input_tokens: number
  output_tokens: number
  customer_charge: string
  promotion_amount: string
  cash_charge: string
  provider_cost: string
  gross_margin: string
  currency: string
  provider_currency: string
  funding_operation_id?: string
  wallet_transaction_id?: string
  ledger_journal_id?: string
  upstream_request_id?: string
  provider_attempt_status?: string
  created_at: string
}

type SubscriptionInvoice = {
  id: string
  invoice_number: string
  invoice_type: string
  status: string
  total_amount: string
  currency: string
  period_start: string
  period_end: string
  payment_provider?: string
  provider_payment_reference?: string
  ledger_journal_id?: string
  created_at: string
}

type MonthlyStatement = {
  organization_id: string
  month: string
  currency: string
  opening_balance: string
  recharge_amount: string
  usage_charge: string
  promotion_amount: string
  subscription_amount: string
  refund_amount: string
  closing_balance: string
  provider_cost: string
  gross_margin: string
  request_count: number
}

type RefundApplication = {
  id: string
  application_number: string
  source_type: 'RECHARGE' | 'SUBSCRIPTION'
  recharge_order_id?: string
  subscription_invoice_id?: string
  requested_amount: string
  currency: string
  unused_cash_amount: string
  used_service_amount: string
  bonus_amount: string
  subscription_fee_amount: string
  provider_irrecoverable_cost: string
  reason: string
  status: string
  review_reason?: string
  created_at: string
}

type InvoiceApplication = {
  id: string
  application_number: string
  invoice_title: string
  tax_identifier?: string
  amount: string
  currency: string
  period_start: string
  period_end: string
  status: string
  processing_reason?: string
  exported_at?: string
  created_at: string
}

const decimalPattern = '^(?:0|[1-9][0-9]{0,29})(?:\\.[0-9]{1,12})?$'
const currentMonth = new Date().toISOString().slice(0, 7)
const monthStart = `${currentMonth}-01`
const monthEndDate = new Date(`${monthStart}T00:00:00Z`)
monthEndDate.setUTCMonth(monthEndDate.getUTCMonth() + 1)
monthEndDate.setUTCDate(0)
const monthEnd = monthEndDate.toISOString().slice(0, 10)

function exactInteger(value: number | string) {
  return String(value).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

function monthRangeEnd(month: string) {
  const start = new Date(`${month}-01T00:00:00Z`)
  if (Number.isNaN(start.valueOf())) return ''
  start.setUTCMonth(start.getUTCMonth() + 1)
  return start.toISOString().slice(0, 10)
}

function isPositiveDecimal(value: string) {
  return /^(?:0|[1-9][0-9]{0,29})(?:\.[0-9]{1,12})?$/.test(value) && /[1-9]/.test(value)
}

function shortID(value?: string) {
  if (!value) return 'Not linked'
  return value.length > 20 ? `${value.slice(0, 12)}…${value.slice(-6)}` : value
}

export function BillingPage() {
  const scope = useProjectScope()
  const client = useQueryClient()
  const toast = useToast()
  const [tab, setTab] = useState<BillingTab>('recharges')
  const [month, setMonth] = useState(currentMonth)
  const [exportKind, setExportKind] = useState<ExportKind>('recharges')
  const [exporting, setExporting] = useState(false)
  const [refundOpen, setRefundOpen] = useState(false)
  const [invoiceOpen, setInvoiceOpen] = useState(false)
  const [refundSource, setRefundSource] = useState<'RECHARGE' | 'SUBSCRIPTION'>('RECHARGE')
  const [refundSourceID, setRefundSourceID] = useState('')
  const [refundAmount, setRefundAmount] = useState('')
  const [refundReason, setRefundReason] = useState('')
  const [refundKey, setRefundKey] = useState(() => crypto.randomUUID())
  const [invoiceTitle, setInvoiceTitle] = useState('')
  const [taxIdentifier, setTaxIdentifier] = useState('')
  const [invoiceAmount, setInvoiceAmount] = useState('')
  const [invoiceCurrency, setInvoiceCurrency] = useState('USD')
  const [periodStart, setPeriodStart] = useState(monthStart)
  const [periodEnd, setPeriodEnd] = useState(monthEnd)
  const [invoiceKey, setInvoiceKey] = useState(() => crypto.randomUUID())

  const organizationID = scope.organizationID
  const balance = useQuery({
    queryKey: ['console-finance-balance', organizationID],
    queryFn: () => api<BalanceComposition>(`/organizations/${organizationID}/finance/balance`),
    enabled: Boolean(organizationID),
  })
  const recharges = useQuery({
    queryKey: ['console-finance-recharges', organizationID],
    queryFn: () => api<unknown>(`/organizations/${organizationID}/recharge-orders`, { query: { limit: 100 } }).then(asPage<RechargeOrder>),
    enabled: Boolean(organizationID),
  })
  const usage = useQuery({
    queryKey: ['console-finance-usage', organizationID, month],
    queryFn: () => api<unknown>(`/organizations/${organizationID}/finance/usage`, { query: { from: `${month}-01T00:00:00Z`, to: `${monthRangeEnd(month)}T00:00:00Z`, limit: 100 } }).then(asPage<FinanceUsage>),
    enabled: Boolean(organizationID && tab === 'usage'),
  })
  const subscriptions = useQuery({
    queryKey: ['console-finance-subscription-orders', organizationID],
    queryFn: () => api<unknown>(`/organizations/${organizationID}/subscription-invoices`, { query: { limit: 100 } }).then(asPage<SubscriptionInvoice>),
    enabled: Boolean(organizationID),
  })
  const statements = useQuery({
    queryKey: ['console-finance-statements', organizationID, month],
    queryFn: () => api<unknown>(`/organizations/${organizationID}/finance/monthly-statements`, { query: { month, limit: 24 } }).then(asPage<MonthlyStatement>),
    enabled: Boolean(organizationID && tab === 'statements'),
  })
  const refunds = useQuery({
    queryKey: ['console-finance-refunds', organizationID],
    queryFn: () => api<unknown>(`/organizations/${organizationID}/refund-applications`, { query: { limit: 100 } }).then(asPage<RefundApplication>),
    enabled: Boolean(organizationID && tab === 'refunds'),
  })
  const invoices = useQuery({
    queryKey: ['console-finance-invoice-applications', organizationID],
    queryFn: () => api<unknown>(`/organizations/${organizationID}/invoice-applications`, { query: { limit: 100 } }).then(asPage<InvoiceApplication>),
    enabled: Boolean(organizationID && tab === 'invoices'),
  })

  const refundableRecharges = useMemo(() => (recharges.data?.items || []).filter((item) => ['CREDITED', 'REFUND_PENDING'].includes(item.status)), [recharges.data])
  const refundableSubscriptions = useMemo(() => (subscriptions.data?.items || []).filter((item) => item.status === 'PAID'), [subscriptions.data])
  const selectedRefundSource = refundSource === 'RECHARGE'
    ? refundableRecharges.find((item) => item.id === refundSourceID)
    : refundableSubscriptions.find((item) => item.id === refundSourceID)

  useEffect(() => {
    if (balance.data?.currency) setInvoiceCurrency(balance.data.currency)
  }, [balance.data?.currency])
  useEffect(() => {
    const candidates = refundSource === 'RECHARGE' ? refundableRecharges : refundableSubscriptions
    if (!candidates.some((item) => item.id === refundSourceID)) setRefundSourceID(candidates[0]?.id || '')
  }, [refundSource, refundSourceID, refundableRecharges, refundableSubscriptions])

  const createRefund = useMutation({
    mutationFn: () => api<RefundApplication>(`/organizations/${organizationID}/refund-applications`, {
      method: 'POST',
      body: JSON.stringify({
        source_type: refundSource,
        ...(refundSource === 'RECHARGE' ? { recharge_order_id: refundSourceID } : { subscription_invoice_id: refundSourceID }),
        amount: refundAmount,
        reason: refundReason.trim(),
        idempotency_key: refundKey,
      }),
    }),
    onSuccess: (item) => {
      setRefundOpen(false)
      setRefundAmount('')
      setRefundReason('')
      setRefundKey(crypto.randomUUID())
      void client.invalidateQueries({ queryKey: ['console-finance-refunds', organizationID] })
      toast(`Refund application ${item.application_number} submitted`)
    },
  })
  const createInvoice = useMutation({
    mutationFn: () => api<InvoiceApplication>(`/organizations/${organizationID}/invoice-applications`, {
      method: 'POST',
      body: JSON.stringify({ invoice_title: invoiceTitle.trim(), tax_identifier: taxIdentifier.trim(), amount: invoiceAmount, currency: invoiceCurrency, period_start: periodStart, period_end: periodEnd, idempotency_key: invoiceKey }),
    }),
    onSuccess: (item) => {
      setInvoiceOpen(false)
      setInvoiceAmount('')
      setInvoiceKey(crypto.randomUUID())
      void client.invalidateQueries({ queryKey: ['console-finance-invoice-applications', organizationID] })
      toast(`Invoice application ${item.application_number} submitted`)
    },
  })

  const download = async (kind = exportKind, selectedMonth = month) => {
    if (!organizationID || exporting) return
    setExporting(true)
    try {
      await apiDownload(`/organizations/${organizationID}/finance/export`, `modeldock-${kind}-${selectedMonth}.csv`, { kind, month: selectedMonth })
      toast('CSV download started')
    } catch (error) {
      toast(error instanceof Error ? error.message : 'CSV download failed', 'danger')
    } finally {
      setExporting(false)
    }
  }

  if (!organizationID && !scope.loading) return <EmptyState title="Select an organization" description="Billing history, balances, refunds, and invoice applications are organization scoped." />
  return <div className="page-stack billing-page">
    <div className="page-header"><div><h1>Billing</h1><p>Trace every payment and Token charge, review monthly statements, and submit refund or invoice applications.</p></div><div className="header-actions"><label className="billing-month"><span>Month</span><input type="month" value={month} onChange={(event) => setMonth(event.target.value)} /></label><label className="billing-export"><span className="sr-only">CSV export type</span><select value={exportKind} onChange={(event) => setExportKind(event.target.value as ExportKind)}><option value="recharges">Recharge history</option><option value="usage">Token usage</option><option value="subscriptions">Subscription orders</option><option value="monthly_statement">Monthly statement</option></select></label><Button onClick={() => void download()} disabled={exporting || !organizationID}><ArrowDownToLine size={14} />{exporting ? 'Downloading…' : 'Download CSV'}</Button></div></div>

    {balance.isLoading && <Panel><Skeleton rows={4} /></Panel>}
    {balance.isError && <ErrorState error={balance.error} onRetry={() => balance.refetch()} />}
    {balance.data && <><div className="metric-grid billing-balance-grid">
      <Metric label="Total available" value={formatMoney(balance.data.aggregate_balance, balance.data.currency)} icon={<WalletCards size={16} />} hint={`As of ${formatDate(balance.data.as_of)}`} />
      <Metric label="Cash balance" value={formatMoney(balance.data.cash_available, balance.data.currency)} icon={<Banknote size={16} />} hint={`${formatMoney(balance.data.refundable_cash, balance.data.currency)} refundable`} />
      <Metric label="Bonus balance" value={formatMoney(balance.data.bonus_available, balance.data.currency)} icon={<Gift size={16} />} hint="Promotional; not cash-refundable" />
      <Metric label="Credit available" value={formatMoney(balance.data.credit_available, balance.data.currency)} icon={<HandCoins size={16} />} hint={`${formatMoney(balance.data.credit_used, balance.data.currency)} used of ${formatMoney(balance.data.credit_limit, balance.data.currency)}`} />
      <Metric label="Reserved" value={formatMoney(balance.data.reserved_balance, balance.data.currency)} icon={<Coins size={16} />} hint="Pending request reservations" />
    </div>{balance.data.attribution_gap !== '0' && balance.data.attribution_gap !== '0.0' && <div className="inline-warning">The wallet contains {formatMoney(balance.data.attribution_gap, balance.data.currency)} of legacy unattributed balance. It remains available but is not represented as a refundable cash lot.</div>}</>}

    <div className="billing-tabs"><Segmented value={tab} onChange={setTab} options={[
      { value: 'recharges', label: 'Recharge history' }, { value: 'usage', label: 'Token usage' }, { value: 'subscriptions', label: 'Subscription orders' },
      { value: 'statements', label: 'Monthly statements' }, { value: 'refunds', label: 'Refunds' }, { value: 'invoices', label: 'Invoices' },
    ]} /></div>

    {tab === 'recharges' && <Panel title="Recharge history" description="A credited order is linked to its immutable wallet transaction and ledger journal.">
      {recharges.isLoading && <Skeleton rows={6} />}{recharges.isError && <ErrorState error={recharges.error} onRetry={() => recharges.refetch()} />}{recharges.isSuccess && !recharges.data.items.length && <EmptyState title="No recharge history" description="Completed recharge orders will appear here." />}
      {!!recharges.data?.items.length && <DataTable rows={recharges.data.items} rowKey={(row) => row.id} columns={[
        { key: 'order', label: 'Order', render: (row) => <div className="primary-cell"><strong>{row.platform_order_no}</strong><small>{row.payment_provider} · {row.provider_order_no || 'Provider reference pending'}</small></div> },
        { key: 'amount', label: 'Amount', render: (row) => <strong>{formatMoney(row.amount, row.currency)}</strong> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'wallet', label: 'Wallet trace', render: (row) => <div className="primary-cell"><code title={row.wallet_transaction_id}>{shortID(row.wallet_transaction_id)}</code><small title={row.ledger_journal_id}>Journal: {shortID(row.ledger_journal_id)}</small></div> },
        { key: 'created', label: 'Paid / created', render: (row) => formatDate(row.paid_at || row.created_at) },
      ]} />}
    </Panel>}

    {tab === 'usage' && <Panel title="Token consumption details" description="Every charge shows the customer debit, wallet evidence, and the Provider request selected for that API call.">
      {usage.isLoading && <Skeleton rows={7} />}{usage.isError && <ErrorState error={usage.error} onRetry={() => usage.refetch()} />}{usage.isSuccess && !usage.data.items.length && <EmptyState title="No Token usage" description="No billable API usage was recorded for the selected month." />}
      {!!usage.data?.items.length && <DataTable rows={usage.data.items} rowKey={(row) => row.request_id} columns={[
        { key: 'request', label: 'Request', render: (row) => <div className="primary-cell"><code title={row.request_id}>{shortID(row.request_id)}</code><small>{row.model} · {row.provider_name || 'Provider unavailable'}</small></div> },
        { key: 'tokens', label: 'Tokens', render: (row) => <div className="primary-cell"><strong>{exactInteger(row.input_tokens)} in · {exactInteger(row.output_tokens)} out</strong><small>{exactInteger(row.cached_input_tokens)} cached input</small></div> },
        { key: 'charge', label: 'User charge', render: (row) => <div className="primary-cell"><strong>{formatMoney(row.customer_charge, row.currency)}</strong><small>{formatMoney(row.cash_charge, row.currency)} cash · {formatMoney(row.promotion_amount, row.currency)} bonus</small></div> },
        { key: 'wallet', label: 'Wallet trace', render: (row) => <div className="primary-cell"><code title={row.wallet_transaction_id}>{shortID(row.wallet_transaction_id)}</code><small title={row.ledger_journal_id}>Journal: {shortID(row.ledger_journal_id)}</small></div> },
        { key: 'provider', label: 'Provider request', render: (row) => <div className="primary-cell"><code title={row.upstream_request_id}>{shortID(row.upstream_request_id)}</code><small><StatusBadge value={row.provider_attempt_status || 'unknown'} /></small></div> },
        { key: 'created', label: 'Created', render: (row) => formatDate(row.created_at) },
      ]} />}
    </Panel>}

    {tab === 'subscriptions' && <Panel title="Subscription orders" description="Subscription fees are separate from metered Token charges and retain their own payment and ledger references.">
      {subscriptions.isLoading && <Skeleton rows={6} />}{subscriptions.isError && <ErrorState error={subscriptions.error} onRetry={() => subscriptions.refetch()} />}{subscriptions.isSuccess && !subscriptions.data.items.length && <EmptyState title="No subscription orders" description="Free and trial periods may not create a paid order." />}
      {!!subscriptions.data?.items.length && <DataTable rows={subscriptions.data.items} rowKey={(row) => row.id} columns={[
        { key: 'invoice', label: 'Order', render: (row) => <div className="primary-cell"><strong>{row.invoice_number}</strong><small>{row.invoice_type}</small></div> },
        { key: 'amount', label: 'Subscription fee', render: (row) => <strong>{formatMoney(row.total_amount, row.currency)}</strong> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'period', label: 'Service period', render: (row) => <div className="primary-cell"><strong>{formatDate(row.period_start)}</strong><small>through {formatDate(row.period_end)}</small></div> },
        { key: 'payment', label: 'Payment trace', render: (row) => <div className="primary-cell"><strong>{row.payment_provider || 'Not paid'}</strong><small title={row.provider_payment_reference}>{shortID(row.provider_payment_reference)}</small></div> },
        { key: 'ledger', label: 'Ledger journal', render: (row) => <code title={row.ledger_journal_id}>{shortID(row.ledger_journal_id)}</code> },
      ]} />}
    </Panel>}

    {tab === 'statements' && <Panel title="Monthly statements" description="Statements summarize settled recharge, usage, subscription, refund, Provider cost, and balance activity by currency.">
      {statements.isLoading && <Skeleton rows={6} />}{statements.isError && <ErrorState error={statements.error} onRetry={() => statements.refetch()} />}{statements.isSuccess && !statements.data.items.length && <EmptyState title="No monthly statement" description="No settled financial activity exists for the selected month." />}
      {!!statements.data?.items.length && <DataTable rows={statements.data.items} rowKey={(row) => `${row.month}-${row.currency}`} columns={[
        { key: 'month', label: 'Month', render: (row) => <div className="primary-cell"><strong>{row.month}</strong><small>{exactInteger(row.request_count)} API requests</small></div> },
        { key: 'opening', label: 'Opening', render: (row) => formatMoney(row.opening_balance, row.currency) },
        { key: 'income', label: 'Recharge / subscription', render: (row) => <div className="primary-cell"><strong>{formatMoney(row.recharge_amount, row.currency)}</strong><small>{formatMoney(row.subscription_amount, row.currency)} subscription</small></div> },
        { key: 'usage', label: 'Usage / bonus', render: (row) => <div className="primary-cell"><strong>{formatMoney(row.usage_charge, row.currency)}</strong><small>{formatMoney(row.promotion_amount, row.currency)} bonus</small></div> },
        { key: 'refund', label: 'Refunds', render: (row) => formatMoney(row.refund_amount, row.currency) },
        { key: 'closing', label: 'Closing', render: (row) => <strong>{formatMoney(row.closing_balance, row.currency)}</strong> },
        { key: 'export', label: '', render: (row) => <Button variant="ghost" size="sm" onClick={() => void download('monthly_statement', row.month)} disabled={exporting}><ArrowDownToLine size={13} />CSV</Button> },
      ]} />}
    </Panel>}

    {tab === 'refunds' && <Panel title="Refund applications" description="Eligibility is assessed server-side across unused cash, consumed service, bonus, subscription fees, and irreversible Provider costs." action={<Button variant="primary" size="sm" onClick={() => setRefundOpen(true)}><RotateCcw size={14} />Request refund</Button>}>
      {refunds.isLoading && <Skeleton rows={6} />}{refunds.isError && <ErrorState error={refunds.error} onRetry={() => refunds.refetch()} />}{refunds.isSuccess && !refunds.data.items.length && <EmptyState title="No refund applications" description="Refund requests and their review status will appear here." />}
      {!!refunds.data?.items.length && <DataTable rows={refunds.data.items} rowKey={(row) => row.id} columns={[
        { key: 'application', label: 'Application', render: (row) => <div className="primary-cell"><strong>{row.application_number}</strong><small>{row.source_type.toLowerCase()} · {row.reason}</small></div> },
        { key: 'requested', label: 'Requested', render: (row) => <strong>{formatMoney(row.requested_amount, row.currency)}</strong> },
        { key: 'assessment', label: 'Eligibility assessment', render: (row) => <div className="refund-breakdown"><span>Unused cash <b>{formatMoney(row.unused_cash_amount, row.currency)}</b></span><span>Used service <b>{formatMoney(row.used_service_amount, row.currency)}</b></span><span>Bonus <b>{formatMoney(row.bonus_amount, row.currency)}</b></span><span>Subscription <b>{formatMoney(row.subscription_fee_amount, row.currency)}</b></span><span>Provider cost <b>{formatMoney(row.provider_irrecoverable_cost, row.currency)}</b></span></div> },
        { key: 'status', label: 'Status', render: (row) => <div className="primary-cell"><StatusBadge value={row.status} />{row.review_reason && <small>{row.review_reason}</small>}</div> },
        { key: 'created', label: 'Submitted', render: (row) => formatDate(row.created_at) },
      ]} />}
    </Panel>}

    {tab === 'invoices' && <Panel title="Invoice applications" description="ModelDock validates and exports applications for finance processing. No tax-system integration or automatic issuance is implied." action={<Button variant="primary" size="sm" onClick={() => setInvoiceOpen(true)}><ReceiptText size={14} />Request invoice</Button>}>
      <div className="inline-note invoice-boundary"><FileText size={14} />Invoice status represents the application workflow only. Approved or exported does not mean a tax invoice was automatically issued.</div>
      {invoices.isLoading && <Skeleton rows={6} />}{invoices.isError && <ErrorState error={invoices.error} onRetry={() => invoices.refetch()} />}{invoices.isSuccess && !invoices.data.items.length && <EmptyState title="No invoice applications" description="Submitted invoice requests and their processing status will appear here." />}
      {!!invoices.data?.items.length && <DataTable rows={invoices.data.items} rowKey={(row) => row.id} columns={[
        { key: 'application', label: 'Application', render: (row) => <div className="primary-cell"><strong>{row.application_number}</strong><small>{row.invoice_title}</small></div> },
        { key: 'amount', label: 'Amount', render: (row) => <strong>{formatMoney(row.amount, row.currency)}</strong> },
        { key: 'period', label: 'Eligible period', render: (row) => `${row.period_start} – ${row.period_end}` },
        { key: 'status', label: 'Status', render: (row) => <div className="primary-cell"><StatusBadge value={row.status} />{row.processing_reason && <small>{row.processing_reason}</small>}</div> },
        { key: 'created', label: 'Submitted', render: (row) => formatDate(row.created_at) },
      ]} />}
    </Panel>}

    <Modal open={refundOpen} onClose={() => setRefundOpen(false)} title="Request a refund" description="Finance reviews the request against immutable payment, wallet, usage, subscription, and Provider-cost evidence." footer={<><Button onClick={() => setRefundOpen(false)}>Cancel</Button><SubmitButton form="refund-application" pending={createRefund.isPending} disabled={!refundSourceID || !isPositiveDecimal(refundAmount) || refundReason.trim().length < 3}>Submit application</SubmitButton></>} wide>
      <Form id="refund-application" className="form-grid" onSubmit={() => createRefund.mutateAsync()}>
        <label><span>Refund category</span><select value={refundSource} onChange={(event) => { setRefundSource(event.target.value as 'RECHARGE' | 'SUBSCRIPTION'); setRefundAmount('') }}><option value="RECHARGE">Unused cash from recharge</option><option value="SUBSCRIPTION">Subscription fee</option></select></label>
        <label><span>Source order</span><select required value={refundSourceID} onChange={(event) => setRefundSourceID(event.target.value)}><option value="">Select an eligible order</option>{refundSource === 'RECHARGE' ? refundableRecharges.map((item) => <option value={item.id} key={item.id}>{item.platform_order_no} · {formatMoney(item.amount, item.currency)}</option>) : refundableSubscriptions.map((item) => <option value={item.id} key={item.id}>{item.invoice_number} · {formatMoney(item.total_amount, item.currency)}</option>)}</select></label>
        <label><span>Requested amount</span><input required type="text" inputMode="decimal" pattern={decimalPattern} value={refundAmount} onChange={(event) => setRefundAmount(event.target.value.trim())} placeholder="0.00" /><small>Exact amount in {selectedRefundSource?.currency || balance.data?.currency || 'the source currency'}; final eligibility is calculated by the server.</small></label>
        <label className="full-span"><span>Reason</span><textarea required minLength={3} maxLength={1000} rows={4} value={refundReason} onChange={(event) => setRefundReason(event.target.value)} placeholder="Explain why this payment or subscription fee should be reviewed." /></label>
        <div className="inline-warning full-span">Used services, promotional balance, and irreversible Provider costs are disclosed separately and are not presented as unused refundable cash.</div>
        {createRefund.isError && <div className="form-error full-span">{createRefund.error instanceof Error ? createRefund.error.message : 'Refund application failed.'}</div>}
      </Form>
    </Modal>

    <Modal open={invoiceOpen} onClose={() => setInvoiceOpen(false)} title="Request an invoice" description="The server validates the amount against settled recharge and paid subscription evidence in the selected period." footer={<><Button onClick={() => setInvoiceOpen(false)}>Cancel</Button><SubmitButton form="invoice-application" pending={createInvoice.isPending} disabled={!invoiceTitle.trim() || !isPositiveDecimal(invoiceAmount) || !periodStart || !periodEnd}>Submit application</SubmitButton></>} wide>
      <Form id="invoice-application" className="form-grid" onSubmit={() => createInvoice.mutateAsync()}>
        <label className="full-span"><span>Invoice title</span><input required maxLength={200} value={invoiceTitle} onChange={(event) => setInvoiceTitle(event.target.value)} /></label>
        <label><span>Tax identifier (optional)</span><input maxLength={100} value={taxIdentifier} onChange={(event) => setTaxIdentifier(event.target.value)} autoComplete="off" /></label>
        <label><span>Amount</span><input required type="text" inputMode="decimal" pattern={decimalPattern} value={invoiceAmount} onChange={(event) => setInvoiceAmount(event.target.value.trim())} placeholder="0.00" /></label>
        <label><span>Currency</span><input required pattern="[A-Z]{3}" maxLength={3} value={invoiceCurrency} onChange={(event) => setInvoiceCurrency(event.target.value.toUpperCase())} /></label>
        <label><span>Period start</span><input required type="date" value={periodStart} onChange={(event) => setPeriodStart(event.target.value)} /></label>
        <label><span>Period end</span><input required type="date" min={periodStart} value={periodEnd} onChange={(event) => setPeriodEnd(event.target.value)} /></label>
        <div className="inline-note full-span">This submits an application for manual finance processing and export. ModelDock does not claim to issue a tax invoice automatically.</div>
        {createInvoice.isError && <div className="form-error full-span">{createInvoice.error instanceof Error ? createInvoice.error.message : 'Invoice application failed.'}</div>}
      </Form>
    </Modal>
  </div>
}
