import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, LifeBuoy, RefreshCw, Send } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import { useProjectScope } from '../lib/project-scope'
import { Button, EmptyState, ErrorState, Panel, Skeleton, StatusBadge } from '../components/ui'

type StatusComponent = { status: string; message?: string }
type StatusResponse = { status: string; updated_at: string; components: { gateway: StatusComponent; dashboard: StatusComponent; billing: StatusComponent; providers: Array<StatusComponent & { name: string }> }; events: Array<{ id: string; summary: string; component: string; status: string; public_message: string; started_at: string; resolved_at?: string }> }

export function StatusPage() {
  const query = useQuery({ queryKey: ['status'], queryFn: () => api<StatusResponse>('/status'), refetchInterval: 30_000 })
  const data = query.data
  const components = data ? [{ name: 'Gateway', ...data.components.gateway }, { name: 'Dashboard', ...data.components.dashboard }, { name: 'Billing', ...data.components.billing }, ...(data.components.providers || []).map((provider) => ({ ...provider, name: `Provider / ${provider.name}` }))] : []
  return <div className="page-stack"><div className="page-header"><div><div className="eyebrow-row"><span className="live-dot" />SYSTEM STATUS</div><h1>ModelDock status</h1><p>Current availability and public incident history.</p></div><Button onClick={() => query.refetch()}><RefreshCw size={14} />Refresh</Button></div>
    {query.isLoading && <Panel><Skeleton rows={7} /></Panel>}{query.isError && <ErrorState error={query.error} onRetry={() => query.refetch()} />}
    {data && <><Panel title={`Overall ${data.status.replaceAll('_', ' ').toLowerCase()}`} description={`Updated ${formatDate(data.updated_at)}`}><div className="status-grid panel-pad">{components.map((component) => <div className="status-component" key={component.name}><span className={component.status === 'OPERATIONAL' ? 'status-icon ok' : 'status-icon warning'}>{component.status === 'OPERATIONAL' ? <CheckCircle2 size={17} /> : <AlertTriangle size={17} />}</span><div><strong>{component.name}</strong><small>{component.message || component.status.replaceAll('_', ' ')}</small></div><StatusBadge value={component.status} /></div>)}</div></Panel><Panel title="Incident history">{data.events.length === 0 ? <EmptyState title="No incidents recorded" /> : <div className="panel-pad">{data.events.map((event) => <div className="member-row" key={event.id}><AlertTriangle size={15} /><div><strong>{event.summary}</strong><small>{event.component} / {formatDate(event.started_at)} / {event.public_message}</small></div><StatusBadge value={event.resolved_at ? 'RESOLVED' : event.status} /></div>)}</div>}</Panel></>}
  </div>
}

type TicketMessage = { id: string; body: string; created_at: string }
type Ticket = { id: string; ticket_number: string; subject: string; status: string; priority: string; request_id?: string; order_id?: string; ledger_journal_id?: string; messages?: TicketMessage[] }

export function SupportPage() {
  const scope = useProjectScope()
  const client = useQueryClient()
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [requestID, setRequestID] = useState('')
  const [orderID, setOrderID] = useState('')
  const [ledgerJournalID, setLedgerJournalID] = useState('')
  const [selectedID, setSelectedID] = useState('')
  const [replyBody, setReplyBody] = useState('')
  const tickets = useQuery({ queryKey: ['support-tickets'], queryFn: () => api<unknown>('/support/tickets', { query: { limit: 100 } }).then(asPage<Ticket>) })
  const selected = useQuery({ queryKey: ['support-ticket', selectedID], queryFn: () => api<Ticket>(`/support/tickets/${selectedID}`), enabled: Boolean(selectedID) })
  const create = useMutation({ mutationFn: () => api<Ticket>('/support/tickets', { method: 'POST', body: JSON.stringify({ subject, body, request_id: requestID.trim() || undefined, order_id: orderID.trim() || undefined, ledger_journal_id: ledgerJournalID.trim() || undefined, organization_id: scope.organizationID || undefined, priority: 'NORMAL' }) }), onSuccess: (ticket) => { setSubject(''); setBody(''); setRequestID(''); setOrderID(''); setLedgerJournalID(''); setSelectedID(ticket.id); void client.invalidateQueries({ queryKey: ['support-tickets'] }) } })
  const reply = useMutation({ mutationFn: () => api(`/support/tickets/${selectedID}/messages`, { method: 'POST', body: JSON.stringify({ body: replyBody }) }), onSuccess: () => { setReplyBody(''); void selected.refetch(); void client.invalidateQueries({ queryKey: ['support-tickets'] }) } })
  const rows = tickets.data?.items || []
  return <div className="page-stack"><div className="page-header"><div><div className="eyebrow-row"><LifeBuoy size={14} />SUPPORT</div><h1>Support tickets</h1><p>Include a request ID to help support trace routing, usage, and billing evidence.</p></div><Button onClick={() => tickets.refetch()}><RefreshCw size={14} />Refresh</Button></div>
    <div className="ticket-layout"><Panel title="Create ticket"><form className="ticket-create panel-pad" onSubmit={(event) => { event.preventDefault(); if (subject.trim() && body.trim()) create.mutate() }}><label><span>Subject</span><input value={subject} onChange={(event) => setSubject(event.target.value)} maxLength={200} required /></label><label><span>Request ID</span><input value={requestID} onChange={(event) => setRequestID(event.target.value)} placeholder="rd_req_..." /></label><label><span>Recharge order UUID</span><input value={orderID} onChange={(event) => setOrderID(event.target.value)} pattern="[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}" placeholder="Optional UUID" /></label><label><span>Ledger journal UUID</span><input value={ledgerJournalID} onChange={(event) => setLedgerJournalID(event.target.value)} pattern="[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}" placeholder="Optional UUID" /></label><label><span>Message</span><textarea value={body} onChange={(event) => setBody(event.target.value)} maxLength={10000} required /></label><Button type="submit" variant="primary" disabled={create.isPending}>Submit ticket</Button>{create.isError && <div className="form-error">{create.error instanceof Error ? create.error.message : 'Ticket could not be created.'}</div>}</form></Panel><Panel title="Your tickets">{tickets.isLoading && <Skeleton rows={5} />}{tickets.isError && <ErrorState error={tickets.error} onRetry={() => tickets.refetch()} />}{tickets.isSuccess && rows.length === 0 && <EmptyState title="No support tickets" />}{rows.map((ticket) => <button key={ticket.id} className={selectedID === ticket.id ? 'ticket-row active' : 'ticket-row'} onClick={() => setSelectedID(ticket.id)}><span><strong>{ticket.ticket_number}</strong><small>{ticket.subject}</small></span><span><StatusBadge value={ticket.priority} /><StatusBadge value={ticket.status} /></span></button>)}</Panel></div>
    {selected.data && <Panel title={`${selected.data.ticket_number} / ${selected.data.subject}`}><div className="ticket-detail panel-pad"><div className="message-thread">{selected.data.messages?.map((message) => <div className="ticket-message" key={message.id}><span>{formatDate(message.created_at)}</span><p>{message.body}</p></div>)}</div><form className="ticket-reply" onSubmit={(event) => { event.preventDefault(); if (replyBody.trim()) reply.mutate() }}><textarea value={replyBody} onChange={(event) => setReplyBody(event.target.value)} maxLength={10000} /><Button type="submit" variant="primary" disabled={reply.isPending || !replyBody.trim()}><Send size={14} />Reply</Button></form></div></Panel>}
  </div>
}
