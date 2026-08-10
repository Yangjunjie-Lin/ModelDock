import { useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Activity, ArrowUpRight, Box, Clock3, Coins, Database, Download, Eye, Filter, Layers3, Search, Sparkles } from 'lucide-react'
import { api, apiDownload, asPage, formatDate, formatMoney, formatNumber } from '../lib/api'
import { Badge, Button, DataTable, type Column, Drawer, EmptyState, ErrorState, Form, Metric, Modal, Pagination, Panel, SearchInput, Segmented, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'
import { consoleV2Paths, useProjectScope } from '../lib/project-scope'

type Row = Record<string, unknown>

export function ModelsPage() {
  const [search, setSearch] = useState('')
  const [capability, setCapability] = useState('')
  const scope = useProjectScope()
  const result = useQuery({ queryKey: ['console-models', scope.projectID, search, capability], queryFn: () => api<unknown>(consoleV2Paths.projectModels(scope.projectID), { query: { project_id: scope.projectID, search, capability, enabled: true } }).then(asPage<Row>), enabled: Boolean(scope.projectID) })
  const rows = (result.data?.items || []).filter((row) => {
    const text = `${row.display_name || ''} ${row.alias || row.id || ''}`.toLowerCase()
    const matchesSearch = !search || text.includes(search.toLowerCase())
    const matchesCapability = !capability || (Array.isArray(row.capabilities) && row.capabilities.map(String).includes(capability))
    return matchesSearch && matchesCapability
  })
  return <div className="page-stack"><div className="page-header"><div><h1>Models</h1><p>{`Explore stable aliases granted to ${scope.project?.name || 'the selected project'}.`}</p></div><a className="button button-default button-md" href="/docs#models">Model guide<ArrowUpRight size={14} /></a></div>{!scope.projectID && !scope.loading && <EmptyState title="Select a project" description="Model aliases are isolated by project route grants." />}{scope.projectID && <section className="resource-panel"><div className="resource-toolbar"><SearchInput value={search} onChange={setSearch} placeholder="Search models and aliases…" /><label className="select-control"><Filter size={14} /><select value={capability} onChange={(event) => setCapability(event.target.value)}><option value="">All capabilities</option><option value="text">Text</option><option value="vision">Vision</option><option value="tools">Tools</option><option value="reasoning">Reasoning</option><option value="embedding">Embedding</option><option value="image">Image</option><option value="audio">Audio</option></select></label></div>{result.isLoading && <div className="panel-pad"><Skeleton rows={9} /></div>}{result.isError && <div className="panel-pad"><ErrorState error={result.error} onRetry={() => result.refetch()} /></div>}{result.isSuccess && rows.length === 0 && <EmptyState title="No models available" description="Your administrator has not granted matching model aliases to this project." />}{rows.length > 0 && <div className="model-grid">{rows.map((row, index) => <article className="model-card" key={String(row.id || index)}><div className="model-card-top"><span className="model-icon"><Layers3 size={17} /></span><StatusBadge value={row.enabled === false ? 'disabled' : 'available'} /></div><h2>{String(row.display_name || row.name || row.id_alias || row.alias || 'Model')}</h2><code>{String(row.id_alias || row.alias || row.provider_model_id || row.id || '—')}</code><p>{String(row.description || 'Stable RelayDock alias granted to this project.')}</p><div className="model-capabilities">{(Array.isArray(row.capabilities) ? row.capabilities : []).map((value) => <Badge key={String(value)} tone="info">{String(value)}</Badge>)}</div><footer><span>Type <strong>{String(row.model_type || 'not published')}</strong></span><span>Context <strong>{row.context_window ? formatNumber(row.context_window, false) : 'Not published'}</strong></span></footer></article>)}</div>}</section>}<div className="pricing-disclaimer"><Coins size={15} /><span>Estimated costs use pricing configured by your RelayDock administrator and may differ from a provider invoice.</span></div></div>
}

type UsageSummary = { requests?: number; input_tokens?: number; cached_input_tokens?: number; output_tokens?: number; estimated_cost?: number; errors?: number; daily?: Row[]; by_model?: Row[] }
export function UsagePage() {
  const [period, setPeriod] = useState<'today' | '7d' | '30d'>('30d')
  const scope = useProjectScope()
  const usageDays = period === 'today' ? 1 : period === '7d' ? 7 : 30
  const result = useQuery({ queryKey: ['console-usage', scope.projectID, period], queryFn: () => api<unknown>(consoleV2Paths.projectUsage(scope.projectID), { query: { project_id: scope.projectID, days: usageDays, ...projectRange(usageDays) } }).then(normalizeUsage), enabled: Boolean(scope.projectID) })
  const data = result.data
  const columns: Column<Row>[] = [
    { key: 'model', label: 'Model', render: (row) => <code className="inline-code">{String(row.model || '—')}</code> },
    { key: 'requests', label: 'Requests', render: (row) => formatNumber(row.requests) },
    { key: 'tokens', label: 'Tokens', render: (row) => formatNumber(row.tokens ?? Number(row.input_tokens || 0) + Number(row.output_tokens || 0)) },
    { key: 'cost', label: 'Estimated cost', render: (row) => <strong>{formatMoney(row.cost)}</strong> },
  ]
  return <div className="page-stack"><div className="page-header"><div><h1>Usage</h1><p>Understand project consumption, reliability, and estimated cost.</p></div><div className="header-actions"><Segmented value={period} onChange={setPeriod} options={[{ value: 'today', label: 'Today' }, { value: '7d', label: '7 days' }, { value: '30d', label: '30 days' }]} /><UsageExportButton /></div></div>{!scope.projectID && !scope.loading && <EmptyState title="Select a project" description="Usage and CSV exports are isolated by project." />}{result.isLoading && <Panel><Skeleton rows={8} /></Panel>}{result.isError && <ErrorState error={result.error} onRetry={() => result.refetch()} />}{data && <><div className="metric-grid usage-metrics"><Metric label="Requests" value={formatNumber(data.requests)} icon={<Activity size={16} />} /><Metric label="Input tokens" value={formatNumber(data.input_tokens)} icon={<Sparkles size={16} />} /><Metric label="Cached input" value={formatNumber(data.cached_input_tokens)} icon={<Database size={16} />} /><Metric label="Output tokens" value={formatNumber(data.output_tokens)} icon={<Box size={16} />} /><Metric label="Estimated cost" value={formatMoney(data.estimated_cost)} icon={<Coins size={16} />} /></div><div className="usage-grid"><Panel className="usage-chart-panel" title="Daily requests" description="Request volume for the selected period">{data.daily?.length ? <UsageBars rows={data.daily} /> : <EmptyState title="No usage in this period" />}</Panel><Panel title="Usage by model" description="Project-granted model aliases">{data.by_model?.length ? <DataTable columns={columns} rows={data.by_model} rowKey={(row) => String(row.model)} /> : <EmptyState title="No model usage" />}</Panel></div><div className="pricing-disclaimer"><Coins size={15} /><span>Estimated cost is calculated using RelayDock configured pricing. It is not a provider invoice.</span></div></>}</div>
}

function UsageExportButton() {
  const scope = useProjectScope()
  const [open, setOpen] = useState(false)
  const today = new Date().toISOString().slice(0, 10)
  const [from, setFrom] = useState(() => new Date(Date.now() - 29 * 86400000).toISOString().slice(0, 10))
  const [to, setTo] = useState(today)
  const toast = useToast()
  const download = useMutation({
    mutationFn: () => apiDownload(consoleV2Paths.projectUsageExport(scope.projectID), `relaydock-${scope.project?.slug || 'project'}-usage-${from}-${to}.csv`, { from, to }),
    onSuccess: () => { setOpen(false); toast('Project usage CSV downloaded') },
  })
  return <><Button onClick={() => setOpen(true)} disabled={!scope.projectID}><Download size={14} />Export CSV</Button><Modal open={open} onClose={() => setOpen(false)} title="Export project usage" description={`Download sanitized accounting rows for ${scope.project?.name || 'the selected project'}.`} footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="console-usage-export" pending={download.isPending}>Download CSV</SubmitButton></>}><Form id="console-usage-export" className="form-grid" onSubmit={() => download.mutateAsync()}><label><span>From (UTC) *</span><input type="date" required max={to} value={from} onChange={(event) => setFrom(event.target.value)} /></label><label><span>To (UTC) *</span><input type="date" required min={from} max={today} value={to} onChange={(event) => setTo(event.target.value)} /></label><div className="inline-note full-span">The export contains request IDs, aliases, status, token counts, estimated cost, and latency. It excludes prompts, responses, cookies, authorization headers, and secrets.</div>{download.isError && <div className="form-error full-span">{download.error instanceof Error ? download.error.message : 'Unable to export usage.'}</div>}</Form></Modal></>
}

function normalizeUsage(value: unknown): UsageSummary {
  const daily = Array.isArray(value) ? value as Row[] : (value && typeof value === 'object' && Array.isArray((value as { daily?: unknown }).daily) ? (value as { daily: Row[] }).daily : value && typeof value === 'object' && Array.isArray((value as { data?: unknown }).data) ? (value as { data: Row[] }).data : [])
  return {
    requests: daily.reduce((sum, row) => sum + Number(row.requests || 0), 0),
    input_tokens: daily.reduce((sum, row) => sum + Number(row.input_tokens || 0), 0),
    cached_input_tokens: daily.reduce((sum, row) => sum + Number(row.cached_input_tokens || 0), 0),
    output_tokens: daily.reduce((sum, row) => sum + Number(row.output_tokens || 0), 0),
    estimated_cost: daily.reduce((sum, row) => sum + Number(row.cost || 0), 0),
    errors: daily.reduce((sum, row) => sum + Number(row.errors || 0), 0),
    daily,
    by_model: value && typeof value === 'object' && Array.isArray((value as { by_model?: unknown }).by_model) ? (value as { by_model: Row[] }).by_model : [],
  }
}

function UsageBars({ rows }: { rows: Row[] }) {
  const max = Math.max(...rows.map((row) => Number(row.requests || 0)), 1)
  return <div className="usage-bars"><div className="usage-bars-chart">{rows.map((row) => <div key={String(row.date)} title={`${row.date}: ${row.requests} requests`}><i style={{ height: `${Math.max(2, Number(row.requests || 0) / max * 100)}%` }} /></div>)}</div><div className="usage-axis"><span>{String(rows[0]?.date || '')}</span><span>{String(rows.at(-1)?.date || '')}</span></div></div>
}

function projectRange(days: number) {
  const to = new Date()
  const from = new Date(to.getTime() - days * 86400000)
  return { from: from.toISOString(), to: to.toISOString() }
}

export function LogsPage() {
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState('')
  const [model, setModel] = useState('')
  const [page, setPage] = useState(1)
  const [detail, setDetail] = useState<Row | null>(null)
  const scope = useProjectScope()
  const result = useQuery({ queryKey: ['console-logs', scope.projectID, search, status, model, page], queryFn: () => api<unknown>(consoleV2Paths.projectLogs(scope.projectID), { query: { project_id: scope.projectID, ...projectRange(30), search, status, model, limit: 20, offset: (page - 1) * 20 } }).then(asPage<Row>), enabled: Boolean(scope.projectID) })
  const columns = useMemo<Column<Row>[]>(() => [
    { key: 'time', label: 'Time', render: (row) => <span className="muted-cell">{formatDate(row.created_at)}</span> },
    { key: 'request_id', label: 'Request ID', render: (row) => <code className="inline-code">{String(row.request_id || '—')}</code> },
    { key: 'model', label: 'Model', render: (row) => <div className="primary-cell"><strong>{String(row.requested_model || '—')}</strong><small>{String(row.resolved_model || '—')}</small></div> },
    { key: 'endpoint', label: 'Endpoint', render: (row) => <code className="inline-code">{String(row.endpoint || '—')}</code> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status_code} /> },
    { key: 'tokens', label: 'Tokens', render: (row) => formatNumber(row.total_tokens) },
    { key: 'latency', label: 'Latency', render: (row) => row.latency_ms === undefined ? '—' : `${formatNumber(row.latency_ms, false)} ms` },
    { key: 'detail', label: '', className: 'action-cell', render: (row) => <Button size="sm" variant="ghost" onClick={() => setDetail(row)}><Eye size={14} /></Button> },
  ], [])
  const rows = (result.data?.items || []).filter((row) => {
    const code = Number(row.status_code || 0)
    const matchesSearch = !search || String(row.request_id || '').toLowerCase().includes(search.toLowerCase())
    const matchesModel = !model || String(row.requested_model || row.model || '').toLowerCase().includes(model.toLowerCase())
    const matchesStatus = !status || (status === 'success' ? code >= 200 && code < 400 : status === '429' ? code === 429 : code >= 400 && code !== 429)
    return matchesSearch && matchesModel && matchesStatus && row.project_id === scope.projectID
  })
  return <div className="page-stack"><div className="page-header"><div><h1>Request logs</h1><p>Inspect sanitized metadata for the selected project. Prompt and response content are not displayed.</p></div></div>{!scope.projectID && !scope.loading && <EmptyState title="Select a project" description="Request metadata is isolated by project." />}{scope.projectID && <section className="resource-panel"><div className="resource-toolbar"><SearchInput value={search} onChange={setSearch} placeholder="Search request ID…" /><div className="toolbar-controls"><label className="select-control"><Filter size={14} /><select value={status} onChange={(event) => setStatus(event.target.value)}><option value="">All statuses</option><option value="success">Success</option><option value="error">Error</option><option value="429">Rate limited</option></select></label><label className="select-control"><Search size={14} /><input className="filter-text" value={model} onChange={(event) => setModel(event.target.value)} placeholder="Model" /></label></div></div>{result.isLoading && <div className="panel-pad"><Skeleton rows={8} /></div>}{result.isError && <div className="panel-pad"><ErrorState error={result.error} onRetry={() => result.refetch()} /></div>}{result.isSuccess && rows.length === 0 && <EmptyState title="No request logs" description="Requests matching the current project and filters will appear here." />}{rows.length > 0 && <DataTable columns={columns} rows={rows} rowKey={(row) => String(row.id || row.request_id)} />}<Pagination page={page} total={rows.length} onChange={setPage} /></section>}<Drawer open={Boolean(detail)} onClose={() => setDetail(null)} title="Request details">{detail && <LogDetails row={detail} />}</Drawer></div>
}

function LogDetails({ row }: { row: Row }) {
  const fields = ['request_id', 'created_at', 'requested_model', 'resolved_model', 'endpoint', 'status_code', 'input_tokens', 'cached_input_tokens', 'output_tokens', 'total_tokens', 'estimated_cost', 'latency_ms', 'ttft_ms', 'error_code']
  return <><div className="request-detail-hero"><Activity size={18} /><div><strong>{String(row.request_id || 'Request')}</strong><StatusBadge value={row.status_code} /></div></div><div className="detail-list">{fields.map((field) => <div key={field}><span>{field.replaceAll('_', ' ')}</span><strong>{field.includes('_at') ? formatDate(row[field]) : String(row[field] ?? '—')}</strong></div>)}</div><div className="inline-note">Only RelayDock request metadata is available in Console. Upstream credential identifiers and provider request IDs are not exposed.</div></>
}

export function NotFoundPage() {
  return <div className="not-found"><span>404</span><h1>Page not found</h1><p>The requested Console page does not exist.</p><a href="/"><Button variant="primary">Return to overview</Button></a></div>
}
