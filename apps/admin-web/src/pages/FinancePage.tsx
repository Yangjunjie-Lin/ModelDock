import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, ShieldCheck, TriangleAlert } from 'lucide-react'
import { addDecimalStrings, api, apiDownload, asPage, formatDate, formatMoneyString, formatNumber } from '../lib/api'
import type { Row } from '../lib/types'
import {
  Badge,
  Button,
  DataTable,
  EmptyState,
  ErrorState,
  Form,
  Modal,
  Pagination,
  Panel,
  SearchInput,
  Segmented,
  Skeleton,
  StatusBadge,
  SubmitButton,
  useToast,
} from '../components/ui'

type FinanceTab = 'payments' | 'anomalies' | 'ledger' | 'refunds' | 'invoices' | 'reports'
type ReportKind = 'provider_cost' | 'user_revenue' | 'gross_margin'
type QueryFilters = { page: number; query: string; status: string }
type FinanceList<T = Row> = { items: T[]; total: number }

const pageSize = 25

const tabs: Array<{ value: FinanceTab; label: string }> = [
  { value: 'payments', label: 'Payment orders' },
  { value: 'anomalies', label: 'Anomalous orders' },
  { value: 'ledger', label: 'Ledger entries' },
  { value: 'refunds', label: 'Refund approvals' },
  { value: 'invoices', label: 'Invoice requests' },
  { value: 'reports', label: 'Financial reports' },
]

const organizationFilterStorage = 'modeldock-admin-finance-organization'
const organizationIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

function useOrganizationFilter() {
  const [organizationID, setOrganizationID] = useState(() => {
    try { return sessionStorage.getItem(organizationFilterStorage) || '' } catch { return '' }
  })
  const change = (value: string) => {
    const next = value.trim()
    setOrganizationID(next)
    try {
      if (next) sessionStorage.setItem(organizationFilterStorage, next)
      else sessionStorage.removeItem(organizationFilterStorage)
    } catch { /* A blocked session store must not prevent finance queries. */ }
  }
  return [organizationID, change] as const
}

function financePage(path: string, filters: QueryFilters, extra: Record<string, string> = {}) {
  return api<unknown>(path, {
    query: {
      limit: pageSize,
      offset: (filters.page - 1) * pageSize,
      query: filters.query || undefined,
      status: filters.status || undefined,
      ...extra,
    },
  }).then((value) => financeList<Row>(value, filters.page))
}

function financeList<T>(value: unknown, page = 1): FinanceList<T> {
  const wrapped = value && typeof value === 'object' ? value as Row : undefined
  const list = asPage<T>(value)
  const rawItems = Array.isArray(value) || Array.isArray(wrapped?.items) || Array.isArray(wrapped?.results) || Array.isArray(wrapped?.data)
  const hasTotal = wrapped?.total !== undefined
  if (rawItems && !hasTotal) return { items: list.items, total: list.items.length === pageSize ? page * pageSize + 1 : (page - 1) * pageSize + list.items.length }
  return list
}

export function FinancePage() {
  const [tab, setTab] = useState<FinanceTab>('payments')
  const [filters, setFilters] = useState<QueryFilters>({ page: 1, query: '', status: '' })
  const [organizationID, setOrganizationID] = useOrganizationFilter()
  const [organizationDraft, setOrganizationDraft] = useState(organizationID)
  const [scopeError, setScopeError] = useState('')
  const changeTab = (value: FinanceTab) => {
    setTab(value)
    setFilters({ page: 1, query: '', status: '' })
  }

  return <div className="page-stack finance-page">
    <div className="page-header">
      <div><span className="eyebrow">FINANCIAL CONTROL</span><h1>Finance</h1><p>Trace customer receipts through immutable wallet entries, review applications, and compare revenue with provider cost.</p></div>
      <AccountingExportButton organizationID={organizationID} />
    </div>
    <div className="finance-control-note"><ShieldCheck size={17} /><div><strong>Posted journals are read-only.</strong><span>Corrections are made only through audited reversal entries; this console never edits a settled ledger.</span></div></div>
    <div className="finance-tabs"><Segmented value={tab} options={tabs} onChange={changeTab} /></div>
    <form className="finance-scope-filter" onSubmit={(event) => { event.preventDefault(); const value = organizationDraft.trim(); if (value && !organizationIDPattern.test(value)) { setScopeError('Enter a valid organization UUID.'); return } setScopeError(''); setOrganizationID(value) }}><label><span>Organization scope</span><input value={organizationDraft} onChange={(event) => setOrganizationDraft(event.target.value)} placeholder="All organizations or organization UUID" />{scopeError && <small>{scopeError}</small>}</label><Button size="sm" type="submit">Apply scope</Button>{organizationID && <Button variant="ghost" size="sm" type="button" onClick={() => { setOrganizationDraft(''); setScopeError(''); setOrganizationID('') }}>Clear scope</Button>}</form>
    {tab === 'payments' && <PaymentOrders filters={filters} onFilters={setFilters} organizationID={organizationID} />}
    {tab === 'anomalies' && <AnomalousOrders filters={filters} onFilters={setFilters} organizationID={organizationID} />}
    {tab === 'ledger' && <LedgerEntries filters={filters} onFilters={setFilters} organizationID={organizationID} />}
    {tab === 'refunds' && <RefundApplications filters={filters} onFilters={setFilters} organizationID={organizationID} />}
    {tab === 'invoices' && <InvoiceApplications filters={filters} onFilters={setFilters} organizationID={organizationID} />}
    {tab === 'reports' && <Reports />}
  </div>
}

function QueryToolbar({ filters, onFilters, statuses = [] }: { filters: QueryFilters; onFilters: (value: QueryFilters) => void; statuses?: string[] }) {
  return <div className="resource-toolbar">
    <SearchInput value={filters.query} onChange={(query) => onFilters({ ...filters, page: 1, query })} placeholder="Search order, organization, or reference…" />
    {statuses.length > 0 && <div className="toolbar-controls"><label className="select-control"><span>Status</span><select value={filters.status} onChange={(event) => onFilters({ ...filters, page: 1, status: event.target.value })}><option value="">All statuses</option>{statuses.map((status) => <option key={status} value={status}>{status.replaceAll('_', ' ')}</option>)}</select></label></div>}
  </div>
}

function PagedSection({ query, filters, onFilters, emptyTitle, children }: { query: { isLoading: boolean; isError: boolean; isSuccess: boolean; error: unknown; data?: { items: Row[]; total: number }; refetch: () => unknown }; filters: QueryFilters; onFilters: (value: QueryFilters) => void; emptyTitle: string; children: (rows: Row[]) => React.ReactNode }) {
  return <section className="resource-panel">
    {query.isLoading && <div className="panel-pad"><Skeleton rows={8} /></div>}
    {query.isError && <div className="panel-pad"><ErrorState error={query.error} onRetry={() => query.refetch()} /></div>}
    {query.isSuccess && !query.data?.items.length && <EmptyState title={emptyTitle} description="No records match the current filters." />}
    {!!query.data?.items.length && children(query.data.items)}
    {query.data && (query.data.total > pageSize || filters.page > 1) && <Pagination page={filters.page} pageSize={pageSize} total={query.data.total} onChange={(page) => onFilters({ ...filters, page })} />}
  </section>
}

function PaymentOrders({ filters, onFilters, organizationID }: { filters: QueryFilters; onFilters: (value: QueryFilters) => void; organizationID: string }) {
  const query = useQuery({ queryKey: ['finance-payment-orders', filters, organizationID], queryFn: () => financePage('/finance/payment-orders', filters, { organization_id: organizationID }) })
  return <div className="finance-section"><QueryToolbar filters={filters} onFilters={onFilters} statuses={['PENDING', 'PAID', 'CREDITED', 'FAILED', 'REFUNDED', 'CHARGEBACK']} /><PagedSection query={query} filters={filters} onFilters={onFilters} emptyTitle="No payment orders">{(rows) => <DataTable rows={rows} rowKey={(row) => String(row.id || row.platform_order_no)} columns={[
    { key: 'order', label: 'Payment order', render: (row) => <div className="primary-cell"><strong>{String(row.platform_order_no || row.order_number || row.id)}</strong><small>{String(row.provider_order_no || 'No provider reference')}</small></div> },
    { key: 'organization', label: 'Organization', render: (row) => <code>{String(row.organization_name || row.organization_id || '—')}</code> },
    { key: 'provider', label: 'Channel', render: (row) => <Badge tone="info">{String(row.payment_provider || row.provider || 'unknown')}</Badge> },
    { key: 'amount', label: 'Amount', render: (row) => <strong>{money(row.amount, row.currency)}</strong> },
    { key: 'ledger', label: 'Wallet trace', render: (row) => row.wallet_transaction_id || row.ledger_journal_id ? <div className="primary-cell"><StatusBadge value="linked" /><small>{shortID(row.wallet_transaction_id || row.ledger_journal_id)}</small></div> : <Badge tone="danger">Unlinked</Badge> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'created', label: 'Created', render: (row) => formatDate(row.created_at) },
  ]} />}</PagedSection></div>
}

function AnomalousOrders({ filters, onFilters, organizationID }: { filters: QueryFilters; onFilters: (value: QueryFilters) => void; organizationID: string }) {
  const query = useQuery({ queryKey: ['finance-anomalous-orders', filters, organizationID], queryFn: () => financePage('/finance/anomalous-orders', filters, { organization_id: organizationID }) })
  return <div className="finance-section"><QueryToolbar filters={filters} onFilters={onFilters} statuses={['OPEN', 'IN_REVIEW', 'RESOLVED', 'ACCEPTED']} /><PagedSection query={query} filters={filters} onFilters={onFilters} emptyTitle="No anomalous orders">{(rows) => <DataTable rows={rows} rowKey={(row) => String(row.id || row.case_key)} columns={[
    { key: 'case', label: 'Exception', render: (row) => <div className="primary-cell"><strong>{anomalyClassification(row)}</strong><small>{String(row.case_key || row.platform_order_no || row.id)}</small></div> },
    { key: 'severity', label: 'Severity', render: (row) => <SeverityBadge value={row.severity || anomalySeverity(row)} /> },
    { key: 'order', label: 'Order', render: (row) => <code>{shortID(row.platform_order_no || row.recharge_order_id)}</code> },
    { key: 'expected', label: 'Expected', render: (row) => money(row.expected_amount || row.amount, row.currency) },
    { key: 'actual', label: 'Evidence', render: (row) => row.actual_amount !== undefined ? money(row.actual_amount, row.currency) : <span className="muted-cell">{row.wallet_transaction_id && row.ledger_journal_id ? 'Linked' : 'Missing link'}</span> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'updated', label: 'Last observed', render: (row) => formatDate(row.updated_at || row.created_at) },
  ]} />}</PagedSection></div>
}

function LedgerEntries({ filters, onFilters, organizationID }: { filters: QueryFilters; onFilters: (value: QueryFilters) => void; organizationID: string }) {
  const query = useQuery({ queryKey: ['finance-ledger-entries', filters, organizationID], queryFn: () => financePage('/finance/ledger-entries', filters, { organization_id: organizationID }) })
  return <div className="finance-section"><QueryToolbar filters={filters} onFilters={onFilters} statuses={['POSTED', 'DRAFT']} /><PagedSection query={query} filters={filters} onFilters={onFilters} emptyTitle="No ledger entries">{(rows) => <DataTable rows={rows} rowKey={(row) => String(row.id || `${row.journal_id}-${row.account_id}-${row.entry_side}`)} columns={[
    { key: 'journal', label: 'Journal', render: (row) => <div className="primary-cell"><strong>{String(row.journal_type || row.JournalType || row.account_name || row.AccountName || 'Journal entry')}</strong><small>{shortID(row.journal_id || row.JournalID || row.id)}</small></div> },
    { key: 'reference', label: 'Reference', render: (row) => <code>{shortID(row.reference || row.Reference || row.external_key || row.ExternalKey)}</code> },
    { key: 'account', label: 'Account', render: (row) => <div className="primary-cell"><strong>{String(row.account_name || row.AccountName || row.account_key || row.AccountKey || '—')}</strong><small>{String(row.account_type || '')}</small></div> },
    { key: 'side', label: 'Side', render: (row) => { const side = ledgerSide(row); return <Badge tone={side === 'DEBIT' ? 'warning' : 'info'}>{side}</Badge> } },
    { key: 'amount', label: 'Amount', render: (row) => <strong>{money(ledgerAmount(row), row.currency || row.Currency)}</strong> },
    { key: 'status', label: 'Journal status', render: (row) => <StatusBadge value={row.status || 'POSTED'} /> },
    { key: 'posted', label: 'Posted', render: (row) => formatDate(row.posted_at || row.PostedAt || row.created_at) },
  ]} />}</PagedSection></div>
}

function RefundApplications({ filters, onFilters, organizationID }: { filters: QueryFilters; onFilters: (value: QueryFilters) => void; organizationID: string }) {
  const query = useQuery({ queryKey: ['finance-refund-applications', filters, organizationID], queryFn: () => financePage('/finance/refund-applications', filters, { organization_id: organizationID }) })
  return <div className="finance-section"><QueryToolbar filters={filters} onFilters={onFilters} statuses={['SUBMITTED', 'UNDER_REVIEW', 'APPROVED', 'REJECTED', 'PROCESSING', 'COMPLETED', 'FAILED']} /><PagedSection query={query} filters={filters} onFilters={onFilters} emptyTitle="No refund applications">{(rows) => <DataTable rows={rows} rowKey={(row) => String(row.id)} columns={[
    { key: 'application', label: 'Application', render: (row) => <div className="primary-cell"><strong>{String(row.application_number || row.id)}</strong><small>{String(row.source_type || '')}</small></div> },
    { key: 'organization', label: 'Organization', render: (row) => <code>{shortID(row.organization_id)}</code> },
    { key: 'requested', label: 'Requested', render: (row) => <strong>{money(row.requested_amount, row.currency)}</strong> },
    { key: 'composition', label: 'Eligibility composition', render: (row) => <AmountComposition row={row} /> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'created', label: 'Submitted', render: (row) => formatDate(row.created_at) },
    { key: 'action', label: '', render: (row) => isReviewable(row.status) ? <DecisionButton kind="refund" row={row} /> : String(row.status).toUpperCase() === 'APPROVED' ? <ProcessRefundButton row={row} /> : <span className="muted-cell">Reviewed</span> },
  ]} />}</PagedSection></div>
}

function InvoiceApplications({ filters, onFilters, organizationID }: { filters: QueryFilters; onFilters: (value: QueryFilters) => void; organizationID: string }) {
	const query = useQuery({ queryKey: ['finance-invoice-applications', filters, organizationID], queryFn: () => financePage('/finance/invoice-applications', filters, { organization_id: organizationID }) })
	return <div className="finance-section"><InvoiceExportButton organizationID={organizationID} /><QueryToolbar filters={filters} onFilters={onFilters} statuses={['SUBMITTED', 'UNDER_REVIEW', 'APPROVED', 'REJECTED', 'EXPORTED', 'CANCELED']} /><PagedSection query={query} filters={filters} onFilters={onFilters} emptyTitle="No invoice applications">{(rows) => <DataTable rows={rows} rowKey={(row) => String(row.id)} columns={[
    { key: 'application', label: 'Application', render: (row) => <div className="primary-cell"><strong>{String(row.application_number || row.id)}</strong><small>{String(row.invoice_title || '—')}</small></div> },
    { key: 'organization', label: 'Organization', render: (row) => <code>{shortID(row.organization_id)}</code> },
    { key: 'period', label: 'Invoice period', render: (row) => <div className="primary-cell"><strong>{String(row.period_start || '—')}</strong><small>to {String(row.period_end || '—')}</small></div> },
    { key: 'amount', label: 'Validated amount', render: (row) => <strong>{money(row.amount, row.currency)}</strong> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'created', label: 'Submitted', render: (row) => formatDate(row.created_at) },
    { key: 'action', label: '', render: (row) => isReviewable(row.status) ? <DecisionButton kind="invoice" row={row} /> : <span className="muted-cell">Processed</span> },
  ]} />}</PagedSection></div>
}

function ProcessRefundButton({ row }: { row: Row }) {
  const [open, setOpen] = useState(false)
  const [evidenceReference, setEvidenceReference] = useState('')
  const client = useQueryClient()
  const toast = useToast()
  const isRecharge = String(row.source_type).toUpperCase() === 'RECHARGE'
  const mutation = useMutation({
    mutationFn: () => api(`/finance/refund-applications/${String(row.id)}/process`, { method: 'POST', body: JSON.stringify({ evidence_reference: evidenceReference.trim(), idempotency_key: crypto.randomUUID() }) }),
    onSuccess: () => { setOpen(false); setEvidenceReference(''); void client.invalidateQueries({ queryKey: ['finance-refund-applications'] }); toast('Manual-transfer refund completed') },
  })
  if (!isRecharge) return <span className="muted-cell">External subscription evidence required</span>
  return <><Button size="sm" onClick={() => setOpen(true)}>Process</Button><Modal open={open} onClose={() => setOpen(false)} title="Process approved refund" description="Only a manual-transfer recharge can complete here. Other channels require a verified payment-adapter result." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form={`process-refund-${row.id}`} pending={mutation.isPending}>Complete refund</SubmitButton></>}>
    <Form id={`process-refund-${row.id}`} className="form-grid" onSubmit={() => mutation.mutateAsync()}><label className="full-span"><span>Evidence reference *</span><input required minLength={3} maxLength={200} value={evidenceReference} onChange={(event) => setEvidenceReference(event.target.value)} placeholder="Bank transfer or approved finance evidence reference" /></label><div className="inline-warning full-span">This posts a refund journal and cash-lot debit; it never edits settled entries.</div>{mutation.isError && <div className="form-error full-span">{mutation.error instanceof Error ? mutation.error.message : 'Refund processing failed.'}</div>}</Form>
  </Modal></>
}

function InvoiceExportButton({ organizationID }: { organizationID: string }) {
  const [pending, setPending] = useState(false)
  const [batchKey, setBatchKey] = useState(() => crypto.randomUUID())
  const client = useQueryClient()
  const toast = useToast()
  const download = async () => {
    setPending(true)
    try {
	  await apiDownload('/finance/invoice-applications/export', 'modeldock-invoice-applications.csv', { organization_id: organizationID || undefined, limit: 100000 }, { 'Idempotency-Key': batchKey })
      await client.invalidateQueries({ queryKey: ['finance-invoice-applications'] })
      toast('Approved invoice applications exported')
	  setBatchKey(crypto.randomUUID())
    } catch (error) {
      toast(error instanceof Error ? error.message : 'Invoice export failed', 'danger')
    } finally { setPending(false) }
  }
  return <Button size="sm" onClick={download} disabled={pending}><Download size={14} />{pending ? 'Exporting…' : 'Export approved CSV'}</Button>
}

function DecisionButton({ kind, row }: { kind: 'refund' | 'invoice'; row: Row }) {
  const [open, setOpen] = useState(false)
  const [decision, setDecision] = useState<'APPROVE' | 'REJECT'>('APPROVE')
  const [reason, setReason] = useState('')
  const [idempotencyKey, setIdempotencyKey] = useState(() => crypto.randomUUID())
  const [confirmed, setConfirmed] = useState(false)
  const client = useQueryClient()
  const toast = useToast()
  const mutation = useMutation({
    mutationFn: () => api(`/finance/${kind}-applications/${String(row.id)}/decision`, {
      method: 'POST',
      body: JSON.stringify({ decision, reason: reason.trim(), idempotency_key: idempotencyKey }),
    }),
    onSuccess: () => {
      setOpen(false)
      setReason('')
      void client.invalidateQueries({ queryKey: [`finance-${kind}-applications`] })
      toast(`${kind === 'refund' ? 'Refund' : 'Invoice'} decision recorded`)
    },
  })
  const title = kind === 'refund' ? 'Review refund application' : 'Process invoice application'
  const startReview = () => { setConfirmed(false); setIdempotencyKey(crypto.randomUUID()); setOpen(true) }
  return <><Button size="sm" onClick={startReview}>Review</Button><Modal open={open} onClose={() => setOpen(false)} title={title} description="The decision, administrator, reason, and idempotency key are retained in the audit trail." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form={`decision-${row.id}`} pending={mutation.isPending} disabled={!confirmed}>Record decision</SubmitButton></>}>
    <Form id={`decision-${row.id}`} className="form-grid" onSubmit={() => mutation.mutateAsync()}>
      {kind === 'refund' && <div className="full-span refund-review-grid"><AmountBox label="Unused cash" value={row.unused_cash_amount} currency={row.currency} tone="success" /><AmountBox label="Used service" value={row.used_service_amount} currency={row.currency} /><AmountBox label="Bonus" value={row.bonus_amount} currency={row.currency} /><AmountBox label="Subscription fee" value={row.subscription_fee_amount} currency={row.currency} /><AmountBox label="Irrecoverable provider cost" value={row.provider_irrecoverable_cost} currency={row.currency} tone="danger" /></div>}
      <label><span>Decision *</span><select value={decision} onChange={(event) => setDecision(event.target.value as 'APPROVE' | 'REJECT')}><option value="APPROVE">Approve</option><option value="REJECT">Reject</option></select></label>
      <label className="full-span"><span>Reason *</span><textarea required minLength={3} maxLength={1000} rows={4} value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Record the evidence and policy basis for this decision" /></label>
      <label className="full-span finance-confirm"><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} required /><span>I verified the source payment, ledger evidence, amount, and policy eligibility.</span></label>
      {kind === 'invoice' && <div className="inline-note full-span">Approval records a validated application only. It does not claim that a tax invoice was issued by an external tax system.</div>}
      {mutation.isError && <div className="form-error full-span">{mutation.error instanceof Error ? mutation.error.message : 'Decision could not be recorded.'}</div>}
    </Form>
  </Modal></>
}

function Reports() {
  const [report, setReport] = useState<ReportKind>('provider_cost')
  const [month, setMonth] = useState(() => new Date().toISOString().slice(0, 7))
  const range = useMemo(() => {
    const from = `${month}-01T00:00:00Z`
    const next = new Date(from)
    if (Number.isNaN(next.valueOf())) return { from: '', to: '' }
    next.setUTCMonth(next.getUTCMonth() + 1)
    return { from, to: next.toISOString() }
  }, [month])
  const query = useQuery({ queryKey: ['finance-reports', report, month], queryFn: () => api<unknown>('/finance/reports', { query: { report, from: range.from, to: range.to } }).then((value) => financeList<Row>(value)) })
  const reportRows = useMemo(() => (query.data?.items || []).filter((row) => report !== 'gross_margin' || String(row.row_type || 'gross_margin') !== 'unallocated_cost'), [query.data, report])
  const unallocatedRows = useMemo(() => report === 'gross_margin' ? (query.data?.items || []).filter((row) => String(row.row_type) === 'unallocated_cost') : [], [query.data, report])
  const reportTotals = useMemo(() => {
    const byCurrency = new Map<string, string[]>()
    for (const row of reportRows) {
      const currency = String(row.currency || 'UNSPECIFIED')
      byCurrency.set(currency, [...(byCurrency.get(currency) || []), String(row.amount || '0')])
    }
    return [...byCurrency.entries()].map(([currency, amounts]) => ({ currency, amount: addDecimalStrings(amounts) }))
  }, [reportRows])
  const unallocatedTotals = useMemo(() => {
    const byCurrency = new Map<string, string[]>()
    for (const row of unallocatedRows) {
      const currency = String(row.currency || 'UNSPECIFIED')
      byCurrency.set(currency, [...(byCurrency.get(currency) || []), String(row.amount || '0')])
    }
    return [...byCurrency.entries()].map(([currency, amounts]) => ({ currency, amount: addDecimalStrings(amounts) }))
  }, [unallocatedRows])
  const requestTotal = useMemo(() => reportRows.reduce((sum, row) => sum + Number(row.requests || 0), 0), [reportRows])
  return <div className="finance-section">
    <Panel className="finance-report-controls" title="Financial reporting" description="Provider cost, customer revenue, and gross margin are separate views of the same request-level evidence.">
      <div className="finance-report-toolbar"><Segmented value={report} options={[{ value: 'provider_cost', label: 'Provider cost' }, { value: 'user_revenue', label: 'User revenue' }, { value: 'gross_margin', label: 'Gross margin' }]} onChange={setReport} /><label><span>Month</span><input type="month" value={month} onChange={(event) => setMonth(event.target.value)} /></label></div>
    </Panel>
    {query.isLoading && <Panel><Skeleton rows={8} /></Panel>}
    {query.isError && <ErrorState error={query.error} onRetry={() => query.refetch()} />}
    {query.isSuccess && query.data.items.length === 0 && <Panel><EmptyState title="No report rows" description="No settled financial activity exists for this reporting period." /></Panel>}
    {query.isSuccess && query.data.items.length > 0 && <>
      <div className="metric-grid finance-report-metrics"><Panel title={report === 'provider_cost' ? 'Provider cost' : report === 'user_revenue' ? 'User revenue' : 'Gross margin'}>{reportTotals.map((total) => <strong className="billing-total" key={total.currency}>{money(total.amount, total.currency)}</strong>)}</Panel><Panel title="Requests"><strong className="billing-total">{formatNumber(requestTotal, false)}</strong></Panel><Panel title="Period"><strong className="billing-total">{month}</strong></Panel></div>
      {unallocatedTotals.length > 0 && <Panel title="Unallocated Provider cost"><div className="inline-warning"><TriangleAlert size={15} /><span>{unallocatedRows.length} evidence group{unallocatedRows.length === 1 ? '' : 's'} require finance review before they can contribute to gross margin.</span></div>{unallocatedTotals.map((total) => <strong className="billing-total" key={total.currency}>{money(total.amount, total.currency)}</strong>)}</Panel>}
      <section className="resource-panel"><DataTable rows={query.data.items} rowKey={(row) => String(row.id || `${row.date || row.month || month}-${row.provider_id || row.provider}-${row.currency}-${row.row_type || report}-${row.unallocated_reason || ''}`)} columns={reportColumns(report)} /></section>
    </>}
  </div>
}

function reportColumns(report: ReportKind) {
  const columns = [
    { key: 'dimension', label: 'Provider', render: (row: Row) => <div className="primary-cell"><strong>{String(row.provider_name || row.provider || row.provider_id || 'Unassigned')}</strong><small>{String(row.model || row.date || row.month || '')}</small></div> },
    { key: 'requests', label: 'Requests', render: (row: Row) => formatNumber(row.request_count || row.requests, false) },
    { key: 'amount', label: report === 'provider_cost' ? 'Provider cost' : report === 'user_revenue' ? 'User revenue' : 'Gross margin', render: (row: Row) => <strong>{money(row.amount || (report === 'provider_cost' ? row.provider_cost : report === 'user_revenue' ? row.user_revenue || row.revenue : row.gross_margin), row.currency)}</strong> },
    { key: 'period', label: 'Period', render: (row: Row) => String(row.date || row.month || row.period || '—') },
  ]
  if (report === 'gross_margin') {
    columns.splice(1, 0, { key: 'allocation', label: 'Cost allocation', render: (row: Row) => String(row.row_type) === 'unallocated_cost'
      ? <Badge tone="warning">Unallocated: {String(row.unallocated_reason || 'UNKNOWN')}</Badge>
      : <Badge tone="success">Snapshot rate</Badge> })
  }
  return columns
}

function AccountingExportButton({ organizationID }: { organizationID: string }) {
  const [from, setFrom] = useState(() => `${new Date().toISOString().slice(0, 7)}-01`)
  const [to, setTo] = useState(() => {
    const next = new Date(`${new Date().toISOString().slice(0, 7)}-01T00:00:00Z`)
    next.setUTCMonth(next.getUTCMonth() + 1)
    return next.toISOString().slice(0, 10)
  })
  const [pending, setPending] = useState(false)
  const toast = useToast()
  const download = async () => {
    setPending(true)
    try {
      await apiDownload('/finance/accounting-export', `modeldock-accounting-${from.slice(0, 7)}.csv`, { from: `${from}T00:00:00Z`, to: `${to}T00:00:00Z`, organization_id: organizationID || undefined, limit: 100000 })
      toast('Accounting export downloaded')
    } catch (error) {
      toast(error instanceof Error ? error.message : 'Accounting export failed', 'danger')
    } finally { setPending(false) }
  }
  const changeMonth = (month: string) => {
    const next = new Date(`${month}-01T00:00:00Z`)
    if (Number.isNaN(next.valueOf())) return
    setFrom(`${month}-01`)
    next.setUTCMonth(next.getUTCMonth() + 1)
    setTo(next.toISOString().slice(0, 10))
  }
  return <div className="header-actions accounting-export"><label><span>Accounting month</span><input type="month" value={from.slice(0, 7)} onChange={(event) => changeMonth(event.target.value)} /></label><Button onClick={download} disabled={pending}><Download size={14} />{pending ? 'Preparing…' : 'Accounting CSV'}</Button></div>
}

function AmountComposition({ row }: { row: Row }) {
  return <div className="amount-composition"><span>Cash {money(row.unused_cash_amount, row.currency)}</span><span>Used {money(row.used_service_amount, row.currency)}</span><span>Bonus {money(row.bonus_amount, row.currency)}</span><span>Provider {money(row.provider_irrecoverable_cost, row.currency)}</span></div>
}

function AmountBox({ label, value, currency, tone }: { label: string; value: unknown; currency: unknown; tone?: 'success' | 'danger' }) {
  return <div className={tone ? `tone-${tone}` : ''}><span>{label}</span><strong>{money(value, currency)}</strong></div>
}

function SeverityBadge({ value }: { value: unknown }) {
  const severity = String(value || 'UNKNOWN').toUpperCase()
  return <Badge tone={severity === 'CRITICAL' || severity === 'HIGH' ? 'danger' : severity === 'MEDIUM' ? 'warning' : 'neutral'}><TriangleAlert size={11} />{severity}</Badge>
}

function anomalyClassification(row: Row) {
  if (row.classification || row.anomaly_type) return String(row.classification || row.anomaly_type)
  if (String(row.status).toUpperCase() === 'CREDITED' && (!row.wallet_transaction_id || !row.ledger_journal_id)) return 'WALLET_LINK_MISSING'
  if (String(row.status).toUpperCase() === 'PAID') return 'WALLET_CREDIT_DELAY'
  return `PAYMENT_${String(row.status || 'ANOMALY').toUpperCase()}`
}

function anomalySeverity(row: Row) {
  return String(row.status).toUpperCase() === 'CREDITED' && (!row.wallet_transaction_id || !row.ledger_journal_id) ? 'CRITICAL' : 'HIGH'
}

function ledgerSide(row: Row) {
  if (row.entry_side) return String(row.entry_side).toUpperCase()
  const debit = String(row.debit ?? row.Debit ?? '0')
  return /^0(?:\.0+)?$/.test(debit) ? 'CREDIT' : 'DEBIT'
}

function ledgerAmount(row: Row) {
  if (row.amount !== undefined) return row.amount
  return ledgerSide(row) === 'DEBIT' ? row.debit ?? row.Debit : row.credit ?? row.Credit
}

function isReviewable(value: unknown) {
  return ['SUBMITTED', 'UNDER_REVIEW'].includes(String(value || '').toUpperCase())
}

function shortID(value: unknown) {
  const text = String(value || '—')
  return text.length > 22 ? `${text.slice(0, 10)}…${text.slice(-8)}` : text
}

function money(value: unknown, currency: unknown) {
  return formatMoneyString(value, currency || 'USD')
}
