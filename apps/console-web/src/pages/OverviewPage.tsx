import { useQuery } from '@tanstack/react-query'
import { Activity, ArrowRight, Braces, Clock3, Coins, Copy, KeyRound, Layers3, Sparkles } from 'lucide-react'
import { Link } from 'react-router-dom'
import { api, formatMoney, formatNumber } from '../lib/api'
import { Badge, Button, EmptyState, ErrorState, Metric, Panel, Skeleton, StatusBadge, useToast } from '../components/ui'
import { useProjectScope } from '../lib/project-scope'

const gatewayBase = `${window.location.origin}/v1`

type Overview = {
  total_requests?: number
  requests_today?: number
  tokens_today?: number
  estimated_cost_today?: number
  error_rate?: number
  monthly_tokens_used?: number
  monthly_token_limit?: number
  monthly_cost_used?: number
  monthly_cost_limit?: number
  active_api_keys?: number
  available_models?: number
  default_model?: string
  request_trend?: Array<{ label?: string; time?: string; requests?: number; value?: number }>
  recent_requests?: Array<Record<string, unknown>>
}

export function OverviewPage() {
  const toast = useToast()
  const scope = useProjectScope()
  const result = useQuery({
    queryKey: ['console-overview', scope.projectID],
    queryFn: () => api<Overview & Record<string, unknown>>('/overview', { query: { project_id: scope.projectID } }).then(normalizeOverview),
    enabled: Boolean(scope.projectID),
    refetchInterval: 30_000,
  })
  const data = result.data
  const code = `from openai import OpenAI\n\nclient = OpenAI(\n    api_key="${'rdk_live_<your-key>'}",\n    base_url="${gatewayBase}"\n)\n\nresponse = client.responses.create(\n    model="${data?.default_model || 'gpt-default'}",\n    input="Hello from RelayDock"\n)`
  return <div className="page-stack">
    <div className="page-header console-welcome"><div><div className="eyebrow-row"><span className="live-dot" />GATEWAY READY</div><h1>Build with one endpoint.</h1><p>Use stable model aliases, RelayDock API keys, and OpenAI-compatible SDKs.</p></div><div className="header-actions"><Link to="/docs"><Button>Read the docs</Button></Link><Link to="/api-keys"><Button variant="primary"><KeyRound size={15} />Create API key</Button></Link></div></div>
    {!scope.projectID && !scope.loading && <EmptyState title="Select a project" description="Choose an organization and project from the sidebar to view isolated usage and keys." />}
    {result.isLoading && <Panel><Skeleton rows={8} /></Panel>}
    {result.isError && <ErrorState error={result.error} onRetry={() => result.refetch()} />}
    {data && <>
      <div className="metric-grid console-metrics"><Metric label={data.requests_today !== undefined ? 'Requests today' : 'Total requests'} value={formatNumber(data.requests_today ?? data.total_requests)} icon={<Activity size={16} />} hint={data.requests_today !== undefined ? 'Current UTC day' : 'Recorded workspace traffic'} /><Metric label="Total tokens" value={formatNumber(data.tokens_today)} icon={<Sparkles size={16} />} hint="Input, cached, and output" /><Metric label="Estimated cost" value={formatMoney(data.estimated_cost_today)} icon={<Coins size={16} />} hint="RelayDock configured pricing" /><Metric label="Error rate" value={data.error_rate === undefined ? '—' : `${Number(data.error_rate).toFixed(2)}%`} icon={<Clock3 size={16} />} hint="Gateway and upstream errors" /></div>
      <div className="console-overview-grid">
        <Panel className="overview-traffic" title="Request activity" description="Resolved requests during the last 24 hours" action={<Badge>24 hours</Badge>}>{data.request_trend?.length ? <MiniChart points={data.request_trend.map((point) => Number(point.requests ?? point.value ?? 0))} /> : <EmptyState title="No request activity" description="Your first API request will appear here." />}</Panel>
        <Panel title="Monthly limits" description="Workspace consumption for the current cycle"><Quota label="Tokens" value={Number(data.monthly_tokens_used || 0)} limit={Number(data.monthly_token_limit || 0)} formatter={formatNumber} /><Quota label="Estimated cost" value={Number(data.monthly_cost_used || 0)} limit={Number(data.monthly_cost_limit || 0)} formatter={formatMoney} /><div className="quota-footer"><span><KeyRound size={14} />{`${formatNumber(data.active_api_keys, false)} active keys`}</span><span><Layers3 size={14} />{`${formatNumber(data.available_models, false)} models`}</span></div></Panel>
        <Panel className="quickstart-panel" title="Quick start" description="OpenAI Python SDK" action={<Button size="sm" onClick={() => { void navigator.clipboard.writeText(code); toast('Code copied') }}><Copy size={13} />Copy</Button>}><pre className="code-block"><code>{code}</code></pre><Link className="run-playground-link" to="/playground"><Braces size={14} />Try it in the Playground<ArrowRight size={13} /></Link></Panel>
        <Panel title="Recent requests" description="Sanitized request metadata" action={<Link className="text-link" to="/logs">View all</Link>}>{data.recent_requests?.length ? <div className="recent-list">{data.recent_requests.map((row, index) => <div key={String(row.request_id || index)}><div><code>{String(row.request_id || '—')}</code><span>{String(row.requested_model || row.model || '—')}</span></div><div><StatusBadge value={row.status_code} /><span>{formatNumber(row.total_tokens)} tok</span><span>{formatNumber(row.latency_ms, false)} ms</span></div></div>)}</div> : <EmptyState title="No recent requests" />}</Panel>
      </div>
    </>}
  </div>
}

function normalizeOverview(value: Overview & Record<string, unknown>): Overview {
  const requests = Number(value.total_requests ?? 0)
  const errors = Number(value.errors ?? 0)
  return {
    ...value,
    total_requests: requests,
    tokens_today: value.tokens_today ?? Number(value.input_tokens ?? 0) + Number(value.cached_input_tokens ?? 0) + Number(value.output_tokens ?? 0),
    estimated_cost_today: value.estimated_cost_today ?? Number(value.estimated_cost ?? 0),
    error_rate: value.error_rate ?? (requests > 0 ? errors / requests * 100 : 0),
  }
}

function MiniChart({ points }: { points: number[] }) {
  const width = 760, height = 200, max = Math.max(...points, 1)
  const coords = points.map((value, index) => `${index / Math.max(1, points.length - 1) * width},${height - value / max * (height - 12)}`)
  return <div className="mini-chart"><svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none"><defs><linearGradient id="consoleArea" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--accent)" stopOpacity=".26" /><stop offset="1" stopColor="var(--accent)" stopOpacity="0" /></linearGradient></defs>{[.25,.5,.75].map((line) => <line key={line} x1="0" x2={width} y1={height * line} y2={height * line} className="chart-gridline" />)}<polygon points={`0,${height} ${coords.join(' ')} ${width},${height}`} fill="url(#consoleArea)" /><polyline points={coords.join(' ')} fill="none" stroke="var(--accent)" strokeWidth="2.2" vectorEffect="non-scaling-stroke" /></svg><div><span>24h ago</span><span>12h ago</span><span>Now</span></div></div>
}

function Quota({ label, value, limit, formatter }: { label: string; value: number; limit: number; formatter: (value: unknown) => string }) {
  const percent = limit > 0 ? Math.min(100, value / limit * 100) : 0
  return <div className="quota-row"><div><strong>{label}</strong><span>{`${formatter(value)} / ${limit > 0 ? formatter(limit) : 'No limit'}`}</span></div><div className="quota-track"><i style={{ width: `${percent}%` }} /></div><small>{limit > 0 ? `${percent.toFixed(1)}% used` : 'No workspace limit configured'}</small></div>
}
