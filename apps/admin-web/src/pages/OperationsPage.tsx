import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, MessageSquareMore, RefreshCw, Send, ShieldAlert } from 'lucide-react'
import { api, asPage, formatDate, formatNumber } from '../lib/api'
import { Button, EmptyState, ErrorState, Panel, Skeleton, StatusBadge } from '../components/ui'

type StatusEvent = { id: string; component: string; status: string; summary: string; public_message: string; started_at: string; resolved_at?: string }
type StatusComponent = { status: string; message?: string }
type StatusResponse = { status: string; updated_at: string; components: { gateway: StatusComponent; dashboard: StatusComponent; billing: StatusComponent; providers: Array<StatusComponent & { name: string }> }; events: StatusEvent[] }
type SLO = { name: string; target_percent: string; window_minutes: number; description: string }
type Observability = { metrics: Record<string, unknown> & { slo_evidence?: Record<string, Record<string, unknown>> }; slos: SLO[]; runtime?: Record<string, unknown> }

export function StatusPage() {
  const status = useQuery({ queryKey: ['public-status'], queryFn: () => api<StatusResponse>('/status/summary'), refetchInterval: 30_000 })
  const observability = useQuery({ queryKey: ['observability'], queryFn: () => api<Observability>('/observability'), refetchInterval: 30_000 })
  const components = status.data ? [
    { name: 'Gateway', ...status.data.components.gateway },
    { name: 'Dashboard', ...status.data.components.dashboard },
    { name: 'Billing', ...status.data.components.billing },
    ...(status.data.components.providers || []).map((provider) => ({ ...provider, name: `Provider / ${provider.name}` })),
  ] : []
  const evidence = observability.data?.metrics.slo_evidence || {}
  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><span className="live-dot" />SERVICE HEALTH</div><h1>Status and SLOs</h1><p>Public-safe component health, reliability objectives, and incident history.</p></div><Button onClick={() => { void status.refetch(); void observability.refetch() }}><RefreshCw size={14} />Refresh</Button></div>
    {(status.isLoading || observability.isLoading) && <Panel><Skeleton rows={7} /></Panel>}
    {status.isError && <ErrorState error={status.error} onRetry={() => status.refetch()} />}
    {status.data && <>
      <Panel title="Current components" description={`Overall ${status.data.status.toLowerCase()} / updated ${formatDate(status.data.updated_at)}`}>
        <div className="status-grid panel-pad">{components.map((component) => <div className="status-component" key={component.name}><span className={component.status === 'OPERATIONAL' ? 'status-icon ok' : 'status-icon warning'}>{component.status === 'OPERATIONAL' ? <CheckCircle2 size={17} /> : <AlertTriangle size={17} />}</span><div><strong>{component.name}</strong><small>{component.message || component.status.replaceAll('_', ' ')}</small></div><StatusBadge value={component.status} /></div>)}</div>
      </Panel>
      <div className="slo-grid">{(observability.data?.slos || []).map((slo) => { const current = evidence[slo.name]; return <Panel key={slo.name} title={slo.name.replaceAll('_', ' ')} description={slo.description}><div className="slo-value"><strong>{String(current?.percent || '100.0000')}%</strong><span>Target {slo.target_percent}% / {slo.window_minutes}m</span></div><small className="muted-cell">{formatNumber(current?.success, false)} successful / {formatNumber(current?.total, false)} observed</small></Panel> })}</div>
      {observability.data?.runtime && <Panel title="Runtime safety" description="Effective non-secret production controls reported by this replica."><div className="status-grid panel-pad">{Object.entries(observability.data.runtime).map(([name, value]) => <div className="status-component" key={name}><CheckCircle2 size={17} /><div><strong>{name.replaceAll('_', ' ')}</strong><small>{String(value)}</small></div></div>)}</div></Panel>}
      <Panel title="Incident history" description="Only public-safe messages are shown on the user status page.">{status.data.events.length === 0 ? <EmptyState title="No incidents recorded" /> : <div className="panel-pad event-list">{status.data.events.map((event) => <div className="member-row" key={event.id}><ShieldAlert size={16} /><div><strong>{event.summary}</strong><small>{event.component} / {formatDate(event.started_at)} / {event.public_message}</small></div><StatusBadge value={event.resolved_at ? 'RESOLVED' : event.status} /></div>)}</div>}</Panel>
    </>}
  </div>
}

type TicketMessage = { id: string; visibility: string; body: string; created_at: string }
type Ticket = { id: string; ticket_number: string; subject: string; status: string; priority: string; request_id?: string; order_id?: string; ledger_journal_id?: string; messages?: TicketMessage[] }

export function SupportPage() {
  const client = useQueryClient()
  const [selectedID, setSelectedID] = useState('')
  const [message, setMessage] = useState('')
  const [internal, setInternal] = useState(false)
  const tickets = useQuery({ queryKey: ['support-tickets'], queryFn: () => api<unknown>('/support/tickets', { query: { limit: 100 } }).then(asPage<Ticket>) })
  const selected = useQuery({ queryKey: ['support-ticket', selectedID], queryFn: () => api<Ticket>(`/support/tickets/${selectedID}`), enabled: Boolean(selectedID) })
  const rows = useMemo(() => tickets.data?.items || [], [tickets.data?.items])
  const active = selected.data
  const reply = useMutation({ mutationFn: () => api(`/support/tickets/${selectedID}/messages`, { method: 'POST', body: JSON.stringify({ body: message, visibility: internal ? 'INTERNAL' : 'PUBLIC' }) }), onSuccess: () => { setMessage(''); void selected.refetch(); void client.invalidateQueries({ queryKey: ['support-tickets'] }) } })
  const update = useMutation({ mutationFn: (status: string) => api(`/support/tickets/${selectedID}`, { method: 'PATCH', body: JSON.stringify({ status, priority: active?.priority || 'NORMAL' }) }), onSuccess: () => { void selected.refetch(); void client.invalidateQueries({ queryKey: ['support-tickets'] }) } })
  const counts = useMemo(() => ({ open: rows.filter((ticket) => !['RESOLVED', 'CLOSED'].includes(ticket.status)).length, urgent: rows.filter((ticket) => ticket.priority === 'URGENT').length }), [rows])
  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><MessageSquareMore size={14} />SUPPORT OPERATIONS</div><h1>Support tickets</h1><p>Trace customer reports to requests, orders, and immutable ledger journals.</p></div><Button onClick={() => tickets.refetch()}><RefreshCw size={14} />Refresh</Button></div>
    <div className="metric-grid compact-metrics"><Panel title="Open"><strong className="billing-total">{counts.open}</strong></Panel><Panel title="Urgent"><strong className="billing-total">{counts.urgent}</strong></Panel></div>
    {tickets.isLoading && <Panel><Skeleton rows={6} /></Panel>}{tickets.isError && <ErrorState error={tickets.error} onRetry={() => tickets.refetch()} />}
    {tickets.isSuccess && rows.length === 0 && <EmptyState title="No support tickets" description="Customer tickets will appear here after submission." />}
    {rows.length > 0 && <div className="ticket-layout"><Panel title="Queue"><div className="ticket-list">{rows.map((ticket) => <button key={ticket.id} className={selectedID === ticket.id ? 'ticket-row active' : 'ticket-row'} onClick={() => setSelectedID(ticket.id)}><span><strong>{ticket.ticket_number}</strong><small>{ticket.subject}</small></span><span><StatusBadge value={ticket.priority} /><StatusBadge value={ticket.status} /></span></button>)}</div></Panel><Panel title={active ? `${active.ticket_number} / ${active.subject}` : 'Ticket detail'}>{selected.isLoading && <Skeleton rows={5} />}{!selectedID && <EmptyState title="Select a ticket" />}{active && <div className="ticket-detail"><div className="ticket-links"><span>Request <code>{active.request_id || '-'}</code></span><span>Order <code>{active.order_id || '-'}</code></span><span>Ledger <code>{active.ledger_journal_id || '-'}</code></span></div><div className="message-thread">{active.messages?.map((item) => <div className={`ticket-message ${item.visibility === 'INTERNAL' ? 'internal' : ''}`} key={item.id}><span>{item.visibility === 'INTERNAL' ? 'Internal note' : 'Reply'} / {formatDate(item.created_at)}</span><p>{item.body}</p></div>)}</div><form className="ticket-reply" onSubmit={(event) => { event.preventDefault(); if (message.trim()) reply.mutate() }}><textarea value={message} onChange={(event) => setMessage(event.target.value)} placeholder="Write a reply" maxLength={10000} /><label><input type="checkbox" checked={internal} onChange={(event) => setInternal(event.target.checked)} />Internal note</label><div><Button type="button" onClick={() => update.mutate('RESOLVED')}>Resolve</Button><Button type="submit" variant="primary" disabled={reply.isPending || !message.trim()}><Send size={14} />Send</Button></div></form></div>}</Panel></div>}
  </div>
}
