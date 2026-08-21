import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarCheck2, Play, RotateCcw, ShieldAlert } from 'lucide-react'
import { api, asPage, formatDate, formatMoneyString, formatNumber } from '../lib/api'
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
  Segmented,
  Skeleton,
  StatusBadge,
  SubmitButton,
  useToast,
} from '../components/ui'

type Tab = 'cases' | 'runs'
type CaseFilters = { page: number; status: string; checkType: string; severity: string }

const pageSize = 25
const checkTypes = [
  'PAYMENT_TO_RECHARGE',
  'RECHARGE_TO_WALLET',
  'USAGE_TO_USER_CHARGE',
  'USAGE_TO_PROVIDER_USAGE',
  'PROVIDER_USAGE_TO_BILL',
  'SUBSCRIPTION_TO_STATE',
]

function financeList(value: unknown, page: number) {
  const wrapped = value && typeof value === 'object' ? value as Row : undefined
  const list = asPage<Row>(value)
  const rawItems = Array.isArray(value) || Array.isArray(wrapped?.items) || Array.isArray(wrapped?.results) || Array.isArray(wrapped?.data)
  const hasTotal = wrapped?.total !== undefined
  if (rawItems && !hasTotal) return { items: list.items, total: list.items.length === pageSize ? page * pageSize + 1 : (page - 1) * pageSize + list.items.length }
  return list
}

export function ReconciliationPage() {
  const [tab, setTab] = useState<Tab>('cases')
  return <div className="page-stack reconciliation-page">
    <div className="page-header"><div><span className="eyebrow">DAILY FINANCIAL CLOSE</span><h1>Reconciliation</h1><p>Every discrepancy is classified, queued, and resolved with retained evidence. Repeated runs do not duplicate observations or repairs.</p></div><RunReconciliationButton /></div>
    <div className="finance-control-note"><ShieldAlert size={17} /><div><strong>Exceptions cannot be silently dismissed.</strong><span>Accepting an exception records the administrator and reason; financial repair requires a reversal of an existing journal.</span></div></div>
    <div className="finance-tabs"><Segmented value={tab} options={[{ value: 'cases', label: 'Exception queue' }, { value: 'runs', label: 'Run history' }]} onChange={setTab} /></div>
    {tab === 'cases' ? <ReconciliationCases /> : <ReconciliationRuns />}
  </div>
}

function ReconciliationCases() {
  const [filters, setFilters] = useState<CaseFilters>({ page: 1, status: 'OPEN', checkType: '', severity: '' })
  const query = useQuery({
    queryKey: ['finance-reconciliation-cases', filters],
    queryFn: () => api<unknown>('/finance/reconciliation-cases', { query: { limit: pageSize, offset: (filters.page - 1) * pageSize, status: filters.status || undefined, check_type: filters.checkType || undefined, severity: filters.severity || undefined } }).then((value) => financeList(value, filters.page)),
  })
  const openCount = useMemo(() => query.data?.items.filter((row) => ['OPEN', 'IN_REVIEW'].includes(String(row.status).toUpperCase())).length || 0, [query.data])
  const criticalCount = useMemo(() => query.data?.items.filter((row) => String(row.severity).toUpperCase() === 'CRITICAL').length || 0, [query.data])
  return <div className="finance-section">
    <div className="metric-grid reconciliation-metrics"><Panel title="Open on this page"><strong className="billing-total">{formatNumber(openCount, false)}</strong></Panel><Panel title="Critical on this page"><strong className="billing-total">{formatNumber(criticalCount, false)}</strong></Panel><Panel title="Checks enabled"><strong className="billing-total">6</strong></Panel></div>
    <section className="resource-panel">
      <div className="resource-toolbar reconciliation-toolbar"><div className="toolbar-controls">
        <FilterSelect label="Status" value={filters.status} options={['OPEN', 'IN_REVIEW', 'RESOLVED', 'ACCEPTED']} allowAll onChange={(status) => setFilters({ ...filters, page: 1, status })} />
        <FilterSelect label="Check" value={filters.checkType} options={checkTypes} allowAll onChange={(checkType) => setFilters({ ...filters, page: 1, checkType })} />
        <FilterSelect label="Severity" value={filters.severity} options={['LOW', 'MEDIUM', 'HIGH', 'CRITICAL']} allowAll onChange={(severity) => setFilters({ ...filters, page: 1, severity })} />
      </div><span className="toolbar-description">Amounts are rendered from decimal strings without JavaScript floating-point conversion.</span></div>
      {query.isLoading && <div className="panel-pad"><Skeleton rows={8} /></div>}
      {query.isError && <div className="panel-pad"><ErrorState error={query.error} onRetry={() => query.refetch()} /></div>}
      {query.isSuccess && query.data.items.length === 0 && <EmptyState title="No reconciliation cases" description="No discrepancies match the selected queue filters." />}
      {!!query.data?.items.length && <DataTable rows={query.data.items} rowKey={(row) => String(row.id || row.case_key)} columns={[
        { key: 'case', label: 'Case', render: (row) => <div className="primary-cell"><strong>{String(row.classification || 'Unclassified')}</strong><small>{String(row.case_key || row.id)}</small></div> },
        { key: 'check', label: 'Automated check', render: (row) => <CheckType value={row.check_type} /> },
        { key: 'severity', label: 'Severity', render: (row) => <SeverityBadge value={row.severity} /> },
        { key: 'amount', label: 'Expected / actual', render: (row) => <div className="primary-cell"><strong>{money(row.expected_amount, row.currency)}</strong><small>actual {money(row.actual_amount, row.currency)}</small></div> },
        { key: 'occurrences', label: 'Occurrences', render: (row) => <div className="primary-cell"><strong>{formatNumber(row.occurrence_count, false)}</strong><small>last {formatDate(row.updated_at)}</small></div> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'action', label: '', render: (row) => ['OPEN', 'IN_REVIEW'].includes(String(row.status).toUpperCase()) ? <ResolveCaseButton row={row} /> : <div className="primary-cell"><span className="muted-cell">Handled</span><small>{shortID(row.handled_by)}</small></div> },
      ]} />}
      {query.data && (query.data.total > pageSize || filters.page > 1) && <Pagination page={filters.page} pageSize={pageSize} total={query.data.total} onChange={(page) => setFilters({ ...filters, page })} />}
    </section>
  </div>
}

function ReconciliationRuns() {
  const [page, setPage] = useState(1)
  const query = useQuery({ queryKey: ['finance-reconciliation-runs', page], queryFn: () => api<unknown>('/finance/reconciliation-runs', { query: { limit: pageSize, offset: (page - 1) * pageSize } }).then((value) => financeList(value, page)), refetchInterval: (state) => state.state.data?.items.some((row) => row.status === 'RUNNING') ? 5_000 : false })
  return <section className="resource-panel">
    {query.isLoading && <div className="panel-pad"><Skeleton rows={8} /></div>}
    {query.isError && <div className="panel-pad"><ErrorState error={query.error} onRetry={() => query.refetch()} /></div>}
    {query.isSuccess && query.data.items.length === 0 && <EmptyState title="No reconciliation runs" description="Scheduled and manual financial close runs will appear here." />}
    {!!query.data?.items.length && <DataTable rows={query.data.items} rowKey={(row) => String(row.id || row.run_key)} columns={[
      { key: 'run', label: 'Run', render: (row) => <div className="primary-cell"><strong>{String(row.run_key || row.id)}</strong><small>{String(row.trigger_source || 'SCHEDULED')}</small></div> },
      { key: 'business_date', label: 'Business date', render: (row) => <strong>{String(row.business_date || '—')}</strong> },
      { key: 'summary', label: 'Summary', render: (row) => <RunSummary summary={row.summary} /> },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'started', label: 'Started', render: (row) => formatDate(row.started_at) },
      { key: 'completed', label: 'Completed', render: (row) => row.completed_at ? formatDate(row.completed_at) : <Badge tone="warning">Running</Badge> },
    ]} />}
    {query.data && (query.data.total > pageSize || page > 1) && <Pagination page={page} pageSize={pageSize} total={query.data.total} onChange={setPage} />}
  </section>
}

function RunReconciliationButton() {
  const [open, setOpen] = useState(false)
  const [businessDate, setBusinessDate] = useState(() => new Date().toISOString().slice(0, 10))
  const client = useQueryClient()
  const toast = useToast()
  const mutation = useMutation({
    mutationFn: () => api('/finance/reconciliation-runs', { method: 'POST', body: JSON.stringify({ business_date: businessDate, idempotency_key: `manual:${businessDate}` }) }),
    onSuccess: () => { setOpen(false); void client.invalidateQueries({ queryKey: ['finance-reconciliation-runs'] }); void client.invalidateQueries({ queryKey: ['finance-reconciliation-cases'] }); toast('Reconciliation run completed') },
  })
  return <><Button onClick={() => setOpen(true)}><Play size={14} />Run reconciliation</Button><Modal open={open} onClose={() => setOpen(false)} title="Run daily reconciliation" description="The business date is the stable idempotency scope. Repeating a completed date returns its durable original run without creating observations, cases, or repairs again." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="start-reconciliation" pending={mutation.isPending}>Run all six checks</SubmitButton></>}>
    <Form id="start-reconciliation" className="form-grid" onSubmit={() => mutation.mutateAsync()}><label className="full-span"><span>Business date *</span><input type="date" required max={new Date().toISOString().slice(0, 10)} value={businessDate} onChange={(event) => setBusinessDate(event.target.value)} /></label><div className="inline-note full-span"><CalendarCheck2 size={14} /> Compares payment channel orders, recharge wallet entries, user charges, provider usage and statements, and subscription state.</div>{mutation.isError && <div className="form-error full-span">{mutation.error instanceof Error ? mutation.error.message : 'Reconciliation failed.'}</div>}</Form>
  </Modal></>
}

function ResolveCaseButton({ row }: { row: Row }) {
  const [open, setOpen] = useState(false)
  const [action, setAction] = useState<'ACCEPT_EXCEPTION' | 'REVERSE_JOURNAL'>('ACCEPT_EXCEPTION')
  const [sourceJournalID, setSourceJournalID] = useState('')
  const [reason, setReason] = useState('')
  const [idempotencyKey, setIdempotencyKey] = useState(() => crypto.randomUUID())
  const client = useQueryClient()
  const toast = useToast()
  const mutation = useMutation({
    mutationFn: () => api(`/finance/reconciliation-cases/${String(row.id)}/resolve`, { method: 'POST', body: JSON.stringify({ action, source_journal_id: action === 'REVERSE_JOURNAL' ? sourceJournalID.trim() : undefined, reason: reason.trim(), idempotency_key: idempotencyKey }) }),
    onSuccess: () => { setOpen(false); setReason(''); setSourceJournalID(''); void client.invalidateQueries({ queryKey: ['finance-reconciliation-cases'] }); toast(action === 'REVERSE_JOURNAL' ? 'Audited reversal posted' : 'Exception acceptance recorded') },
  })
  const startResolution = () => {
    const details = row.details && typeof row.details === 'object' ? row.details as Row : {}
    setSourceJournalID(String(details.journal_id || details.ledger_journal_id || ''))
    setIdempotencyKey(crypto.randomUUID())
    setOpen(true)
  }
  return <><Button size="sm" onClick={startResolution}>Resolve</Button><Modal open={open} onClose={() => setOpen(false)} title="Resolve reconciliation case" description="Choose an explicit evidence-backed outcome. A posted journal is never edited in place." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form={`resolve-${row.id}`} pending={mutation.isPending}>Record resolution</SubmitButton></>}>
    <Form id={`resolve-${row.id}`} className="form-grid" onSubmit={() => mutation.mutateAsync()}>
      <div className="full-span reconciliation-case-summary"><div><span>Classification</span><strong>{String(row.classification)}</strong></div><div><span>Expected</span><strong>{money(row.expected_amount, row.currency)}</strong></div><div><span>Actual</span><strong>{money(row.actual_amount, row.currency)}</strong></div></div>
      <label className="full-span"><span>Resolution *</span><select value={action} onChange={(event) => setAction(event.target.value as 'ACCEPT_EXCEPTION' | 'REVERSE_JOURNAL')}><option value="ACCEPT_EXCEPTION">Accept documented exception</option><option value="REVERSE_JOURNAL">Post reversal journal</option></select><small>Accepting an exception does not alter any financial balance.</small></label>
      {action === 'REVERSE_JOURNAL' && <label className="full-span"><span>Source journal ID *</span><input required value={sourceJournalID} onChange={(event) => setSourceJournalID(event.target.value)} placeholder="Journal UUID to reverse" /><small>The backend creates a linked, balanced reversal and rejects direct mutation of the posted source.</small></label>}
      <label className="full-span"><span>Handling reason *</span><textarea required minLength={3} maxLength={1000} rows={4} value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Describe the evidence and why this resolution is appropriate" /></label>
      {action === 'REVERSE_JOURNAL' && <div className="inline-warning full-span"><RotateCcw size={14} />This creates a new reversal journal. It does not delete or change the original journal.</div>}
      {mutation.isError && <div className="form-error full-span">{mutation.error instanceof Error ? mutation.error.message : 'Resolution failed.'}</div>}
    </Form>
  </Modal></>
}

function FilterSelect({ label, value, options, allowAll, onChange }: { label: string; value: string; options: string[]; allowAll?: boolean; onChange: (value: string) => void }) {
  return <label className="select-control"><span>{label}</span><select value={value} onChange={(event) => onChange(event.target.value)}>{allowAll && <option value="">All</option>}{options.map((item) => <option value={item} key={item}>{item.replaceAll('_', ' ')}</option>)}</select></label>
}

function CheckType({ value }: { value: unknown }) {
  const label = String(value || 'UNKNOWN').replaceAll('_', ' ')
  return <Badge tone="info">{label}</Badge>
}

function SeverityBadge({ value }: { value: unknown }) {
  const severity = String(value || 'UNKNOWN').toUpperCase()
  return <Badge tone={severity === 'CRITICAL' || severity === 'HIGH' ? 'danger' : severity === 'MEDIUM' ? 'warning' : 'neutral'}>{severity}</Badge>
}

function RunSummary({ summary }: { summary: unknown }) {
  const value = summary && typeof summary === 'object' ? summary as Row : {}
  return <div className="run-summary"><span><b>{formatNumber(value.matched || value.matched_count || 0, false)}</b> matched</span><span><b>{formatNumber(value.mismatched || value.mismatch_count || value.cases_created || 0, false)}</b> exceptions</span><span><b>{formatNumber(value.errors || value.error_count || 0, false)}</b> errors</span></div>
}

function money(value: unknown, currency: unknown) { return formatMoneyString(value, currency || 'USD') }
function shortID(value: unknown) { const text = String(value || '—'); return text.length > 18 ? `${text.slice(0, 8)}…${text.slice(-6)}` : text }
