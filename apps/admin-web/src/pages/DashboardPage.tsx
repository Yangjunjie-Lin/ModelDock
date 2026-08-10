import { useQuery } from '@tanstack/react-query'
import { Activity, ArrowUpRight, Clock3, Coins, KeyRound, ShieldCheck, TriangleAlert, Zap } from 'lucide-react'
import { Link } from 'react-router-dom'
import { api, formatMoney, formatNumber } from '../lib/api'
import type { DashboardSummary } from '../lib/types'
import { Badge, Button, EmptyState, ErrorState, Metric, Panel, Skeleton, StatusBadge } from '../components/ui'

export function DashboardPage() {
  const summary = useQuery({
    queryKey: ['admin-dashboard'],
    queryFn: () => api<DashboardSummary & Record<string, unknown>>('/dashboard').then(normalizeDashboard),
    refetchInterval: 30_000,
  })
  const data = summary.data

  return (
    <div className="page-stack">
      <div className="page-header"><div><div className="eyebrow-row"><span className="live-dot" />LIVE OPERATIONS</div><h1>Control plane overview</h1><p>Gateway traffic, credential health, and routing performance at a glance.</p></div><div className="header-actions"><span className="last-updated">Updated {summary.dataUpdatedAt ? new Date(summary.dataUpdatedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '—'}</span><Button onClick={() => summary.refetch()}>Refresh</Button></div></div>
      {summary.isLoading && <Panel><Skeleton rows={7} /></Panel>}
      {summary.isError && <ErrorState error={summary.error} onRetry={() => summary.refetch()} />}
      {data && <>
        <div className="metric-grid">
          <Metric label="Requests today" value={formatNumber(data.requests_today)} icon={<Activity size={16} />} hint={<span className="positive"><ArrowUpRight size={13} /> Current UTC day</span>} />
          <Metric label="Success rate" value={data.success_rate === undefined ? '—' : `${Number(data.success_rate).toFixed(2)}%`} icon={<ShieldCheck size={16} />} hint="Completed without gateway or upstream errors" />
          <Metric label="Active credentials" value={formatNumber(data.active_credentials, false)} icon={<KeyRound size={16} />} hint={<span>{`${formatNumber(data.healthy_credentials, false)} healthy · ${formatNumber(data.rate_limited_credentials, false)} limited`}</span>} />
          <Metric label="Estimated cost" value={formatMoney(data.estimated_cost)} icon={<Coins size={16} />} hint="RelayDock configured pricing" />
          <Metric label="Tokens today" value={formatNumber(data.today_tokens)} icon={<Activity size={16} />} hint="Current UTC day" />
          <Metric label="Cost today" value={formatMoney(data.today_cost)} icon={<Coins size={16} />} hint="Configured model pricing" />
          <Metric label="Estimated savings" value={formatMoney(data.savings_amount)} icon={<Coins size={16} />} hint="Smart routes vs highest-cost eligible model" />
          <Metric label="Average latency" value={data.average_latency_ms === undefined ? '—' : `${formatNumber(data.average_latency_ms, false)} ms`} icon={<Zap size={16} />} hint={`P95 ${data.p95_latency_ms === undefined ? '—' : `${formatNumber(data.p95_latency_ms, false)} ms`}`} />
          <Metric label="Total requests" value={formatNumber(data.total_requests)} icon={<Clock3 size={16} />} hint="All recorded gateway traffic" />
          <Metric label="Input tokens" value={formatNumber(data.total_input_tokens)} icon={<Activity size={16} />} hint="All recorded input tokens" />
          <Metric label="Cached input" value={formatNumber(data.total_cached_tokens)} icon={<Activity size={16} />} hint="Provider-reported cached tokens" />
          <Metric label="Output tokens" value={formatNumber(data.total_output_tokens)} icon={<Activity size={16} />} hint="All recorded output tokens" />
        </div>

        {Number(data.active_credentials || 0) === 0 && <div className="onboarding-callout"><div><span className="callout-icon"><KeyRound size={19} /></span><div><strong>No credentials configured</strong><p>Add an authorized OpenAI API credential before routing traffic.</p></div></div><Button variant="primary" onClick={() => { window.location.href = '/credentials' }}>Add your first credential</Button></div>}

        <div className="dashboard-grid">
          <Panel className="chart-panel wide" title="Request volume" description="Requests resolved by the gateway over the last 24 hours" action={<Badge tone="neutral">24 hours</Badge>}>
            {data.request_trend?.length ? <LineChart points={data.request_trend.map((point) => Number(point.value ?? point.requests ?? 0))} labels={data.request_trend.map((point) => String(point.label ?? point.time ?? ''))} /> : <EmptyState title="No request activity" description="Traffic will appear here after the gateway receives requests." />}
          </Panel>
          <Panel className="chart-panel" title="Model distribution" description="Share of resolved requests">
            {data.model_distribution?.length ? <Distribution items={data.model_distribution} /> : <EmptyState title="No model usage" />}
          </Panel>
          <Panel className="chart-panel wide" title="Token throughput" description="Input, cached input, and output tokens">
            {data.token_trend?.length ? <TokenBars points={data.token_trend} /> : <EmptyState title="No token usage" />}
          </Panel>
          <Panel className="alert-panel" title="Active alerts" action={<Link className="text-link" to="/alerts">View all</Link>}>
            {data.alerts?.length ? <div className="alert-list">{data.alerts.slice(0, 4).map((alert, index) => <div className="alert-row" key={alert.id || index}><span className={`alert-symbol ${alert.severity || 'warning'}`}><TriangleAlert size={15} /></span><div><strong>{alert.title || 'System alert'}</strong><p>{alert.message || 'Review this condition in Alerts.'}</p></div><StatusBadge value={alert.severity} /></div>)}</div> : <EmptyState title="No active alerts" description="All monitored conditions are within configured thresholds." />}
          </Panel>
        </div>
      </>}
    </div>
  )
}

function normalizeDashboard(value: DashboardSummary & Record<string, unknown>): DashboardSummary {
  return {
    ...value,
    total_input_tokens: value.total_input_tokens ?? Number(value.input_tokens ?? 0),
    total_cached_tokens: value.total_cached_tokens ?? Number(value.cached_input_tokens ?? 0),
    total_output_tokens: value.total_output_tokens ?? Number(value.output_tokens ?? 0),
  }
}

function LineChart({ points, labels }: { points: number[]; labels: string[] }) {
  const width = 800; const height = 220; const padding = 12
  const max = Math.max(...points, 1); const min = Math.min(...points, 0); const range = Math.max(1, max - min)
  const coords = points.map((value, index) => ({ x: padding + (index / Math.max(1, points.length - 1)) * (width - padding * 2), y: padding + (1 - (value - min) / range) * (height - padding * 2) }))
  const line = coords.map((point) => `${point.x},${point.y}`).join(' ')
  const area = `${padding},${height - padding} ${line} ${width - padding},${height - padding}`
  return <div className="line-chart"><svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label="Request volume trend"><defs><linearGradient id="areaFill" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="var(--accent)" stopOpacity=".28" /><stop offset="100%" stopColor="var(--accent)" stopOpacity="0" /></linearGradient></defs>{[0.2, 0.4, 0.6, 0.8].map((value) => <line key={value} x1="0" x2={width} y1={height * value} y2={height * value} className="chart-gridline" />)}<polygon points={area} fill="url(#areaFill)" /><polyline points={line} fill="none" stroke="var(--accent)" strokeWidth="2.5" vectorEffect="non-scaling-stroke" />{coords.filter((_, index) => index % Math.ceil(points.length / 6) === 0).map((point, index) => <circle key={index} cx={point.x} cy={point.y} r="3" fill="var(--surface)" stroke="var(--accent)" strokeWidth="2" vectorEffect="non-scaling-stroke" />)}</svg><div className="chart-labels">{labels.filter((_, index) => index % Math.ceil(labels.length / 5) === 0).slice(0, 5).map((label) => <span key={label}>{label}</span>)}</div></div>
}

function Distribution({ items }: { items: NonNullable<DashboardSummary['model_distribution']> }) {
  const total = items.reduce((sum, item) => sum + Number(item.value ?? item.requests ?? 0), 0) || 1
  return <div className="distribution"><div className="donut" style={{ background: conicGradient(items.map((item) => Number(item.value ?? item.requests ?? 0) / total)) }}><span><strong>{items.length}</strong><small>models</small></span></div><div className="legend">{items.map((item, index) => { const value = Number(item.value ?? item.requests ?? 0); return <div key={item.model || item.label || index}><span className={`legend-dot c${index % 4}`} /><p><strong>{item.model || item.label || 'Unknown'}</strong><small>{Math.round(value / total * 100)}%</small></p></div> })}</div></div>
}

function conicGradient(parts: number[]) {
  const colors = ['#6d6cff', '#2dc9a4', '#54a4ff', '#b47cff']; let cursor = 0
  const stops = parts.map((part, index) => { const start = cursor; cursor += part * 100; return `${colors[index % colors.length]} ${start}% ${cursor}%` })
  return `conic-gradient(${stops.join(',')})`
}

function TokenBars({ points }: { points: NonNullable<DashboardSummary['token_trend']> }) {
  const max = Math.max(...points.flatMap((point) => [Number(point.input || 0), Number(point.cached || 0), Number(point.output || point.value || 0)]), 1)
  return <div className="token-chart"><div className="bars">{points.map((point, index) => <div className="bar-group" key={index} title={String(point.label || point.time || '')}><i className="bar-input" style={{ height: `${Math.max(2, Number(point.input || 0) / max * 100)}%` }} /><i className="bar-cache" style={{ height: `${Math.max(2, Number(point.cached || 0) / max * 100)}%` }} /><i className="bar-output" style={{ height: `${Math.max(2, Number(point.output || point.value || 0) / max * 100)}%` }} /></div>)}</div><div className="token-legend"><span><i className="bar-input" />Input</span><span><i className="bar-cache" />Cached</span><span><i className="bar-output" />Output</span></div></div>
}
