import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw, RotateCcw, Send, ShieldCheck, Trash2, Webhook } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import { type WebhookDelivery, type WebhookEndpoint, useAdminTenantScope, v2Paths } from '../lib/v2'
import { TenantSelector } from '../components/TenantSelector'
import { Badge, Button, DataTable, type Column, EmptyState, ErrorState, Form, Modal, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

const eventOptions = ['webhook.test', 'budget.warning', 'budget.exceeded', 'api_key.rotated']

export function WebhooksPage() {
  const scope = useAdminTenantScope()
  const [createOpen, setCreateOpen] = useState(false)
  const [deliveryStatus, setDeliveryStatus] = useState('')
  const [form, setForm] = useState({ name: '', url: '', secret: '', event_types: ['webhook.test'] })
  const client = useQueryClient()
  const toast = useToast()
  const projectID = scope.projectID
  const endpoints = useQuery({
    queryKey: ['v2-webhooks', projectID],
    queryFn: () => api<unknown>(v2Paths.projectWebhooks(projectID)).then(asPage<WebhookEndpoint>),
    enabled: Boolean(projectID),
  })
  const deliveries = useQuery({
    queryKey: ['v2-webhook-deliveries', projectID, deliveryStatus],
    queryFn: () => api<unknown>(v2Paths.projectWebhookDeliveries(projectID), { query: { status: deliveryStatus, limit: 50 } }).then(asPage<WebhookDelivery>),
    enabled: Boolean(projectID),
    refetchInterval: projectID ? 10_000 : false,
  })
  const create = useMutation({
    mutationFn: () => api<{ webhook: WebhookEndpoint; signing_secret: string }>(v2Paths.projectWebhooks(projectID), { method: 'POST', body: JSON.stringify({ name: form.name, url: form.url, signing_secret: form.secret, event_types: form.event_types, enabled: true }) }),
    onSuccess: () => { setCreateOpen(false); setForm({ name: '', url: '', secret: '', event_types: ['webhook.test'] }); void client.invalidateQueries({ queryKey: ['v2-webhooks', projectID] }); toast('Webhook endpoint created') },
  })
  const test = useMutation({
    mutationFn: (webhookID: string) => api(v2Paths.projectWebhookTest(projectID, webhookID), { method: 'POST' }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['v2-webhook-deliveries', projectID] }); toast('Test webhook queued') },
  })
  const remove = useMutation({
    mutationFn: (webhookID: string) => api(v2Paths.projectWebhook(projectID, webhookID), { method: 'DELETE' }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['v2-webhooks', projectID] }); toast('Webhook endpoint disabled') },
  })
  const retry = useMutation({
    mutationFn: (deliveryID: string) => api(v2Paths.projectWebhookRetry(projectID, deliveryID), { method: 'POST' }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['v2-webhook-deliveries', projectID] }); toast('Webhook delivery queued for retry') },
  })

  const endpointRows = endpoints.data?.items || []
  const endpointColumns: Column<WebhookEndpoint>[] = [
    { key: 'name', label: 'Endpoint', render: (row) => <div className="primary-cell"><strong>{row.name}</strong><small>{row.url}</small></div> },
    { key: 'events', label: 'Events', render: (row) => <div className="badge-row">{(row.event_types || []).map((event) => <Badge key={event}>{event}</Badge>)}</div> },
    { key: 'secret', label: 'Signing secret', render: (row) => <code className="inline-code">••••••••{row.secret_last4 || '••••'}</code> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.enabled ? 'enabled' : 'disabled'} /> },
    { key: 'last', label: 'Last delivery', render: (row) => <span className="muted-cell">{formatDate(row.last_delivery_at)}</span> },
    { key: 'actions', label: '', className: 'action-cell', render: (row) => <div className="row-actions"><Button size="sm" onClick={() => test.mutate(row.id)} disabled={!row.enabled || test.isPending}><Send size={13} />Test</Button><Button size="sm" variant="ghost" onClick={() => remove.mutate(row.id)} aria-label="Disable webhook"><Trash2 size={13} /></Button></div> },
  ]
  const deliveryRows = deliveries.data?.items || []
  const deliveryColumns: Column<WebhookDelivery>[] = [
    { key: 'event', label: 'Event', render: (row) => <div className="primary-cell"><strong>{row.event_type}</strong><code>{row.event_id || row.id}</code></div> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'attempts', label: 'Attempts', render: (row) => `${row.attempts} / ${row.max_attempts}` },
    { key: 'http', label: 'Last HTTP', render: (row) => row.last_http_status ? <StatusBadge value={row.last_http_status} /> : <span className="muted-cell">—</span> },
    { key: 'error', label: 'Last result', className: 'wide-cell', render: (row) => <span title={row.last_error}>{row.last_error || (row.delivered_at ? `Delivered ${formatDate(row.delivered_at)}` : `Available ${formatDate(row.available_at)}`)}</span> },
    { key: 'actions', label: '', className: 'action-cell', render: (row) => <Button size="sm" variant="ghost" disabled={row.status !== 'DEAD'} onClick={() => retry.mutate(row.id)}><RotateCcw size={13} />Retry</Button> },
  ]

  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><span className="live-dot" />SIGNED EVENT DELIVERY</div><h1>Webhooks</h1><p>Deliver project-scoped budget and key lifecycle events through a durable outbox.</p></div><div className="header-actions"><Button onClick={() => { void endpoints.refetch(); void deliveries.refetch() }} disabled={!projectID}><RefreshCw size={14} />Refresh</Button><Button variant="primary" onClick={() => setCreateOpen(true)} disabled={!projectID}><Webhook size={15} />Add endpoint</Button></div></div>
    <TenantSelector organizations={scope.organizationRows} organizationID={scope.organizationID} onOrganizationChange={scope.setOrganizationID} projects={scope.projectRows} projectID={scope.projectID} onProjectChange={scope.setProjectID} projectRequired />
    <div className="webhook-security"><ShieldCheck size={17} /><div><strong>HMAC-SHA256 signed, at-least-once delivery</strong><p>Verify the exact request body with the timestamp and signature headers, then deduplicate by event ID. Redirects are never followed.</p></div><Badge tone="success">V2</Badge></div>
    {!projectID && <EmptyState title="Select a project" description="Webhook endpoints and delivery attempts are isolated by project." />}
    {projectID && <><section className="resource-panel"><div className="panel-heading webhook-heading"><div><h2>Endpoints</h2><p>Signing secrets are encrypted and never returned.</p></div></div>{endpoints.isLoading && <div className="panel-pad"><Skeleton rows={5} /></div>}{endpoints.isError && <div className="panel-pad"><ErrorState error={endpoints.error} onRetry={() => endpoints.refetch()} /></div>}{endpoints.isSuccess && endpointRows.length === 0 && <EmptyState title="No webhook endpoints" description="Add an HTTPS receiver to subscribe to project events." action={<Button variant="primary" onClick={() => setCreateOpen(true)}>Add endpoint</Button>} />}{endpointRows.length > 0 && <DataTable columns={endpointColumns} rows={endpointRows} rowKey={(row) => row.id} />}</section>
      <section className="resource-panel"><div className="resource-toolbar"><div><strong>Delivery outbox</strong><p className="toolbar-description">Pending, delivered, retrying, and dead-letter events.</p></div><label className="select-control"><select value={deliveryStatus} onChange={(event) => setDeliveryStatus(event.target.value)}><option value="">All statuses</option><option value="PENDING">Pending</option><option value="RETRY">Retrying</option><option value="DELIVERED">Delivered</option><option value="DEAD">Dead letter</option></select></label></div>{deliveries.isLoading && <div className="panel-pad"><Skeleton rows={6} /></div>}{deliveries.isError && <div className="panel-pad"><ErrorState error={deliveries.error} onRetry={() => deliveries.refetch()} /></div>}{deliveries.isSuccess && deliveryRows.length === 0 && <EmptyState title="No webhook deliveries" description="Queued tests and subscribed project events will appear here." />}{deliveryRows.length > 0 && <DataTable columns={deliveryColumns} rows={deliveryRows} rowKey={(row) => row.id} />}</section></>}

    <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="Add webhook endpoint" description="Production endpoints must use HTTPS and resolve to a public network address." footer={<><Button onClick={() => setCreateOpen(false)}>Cancel</Button><SubmitButton form="create-webhook" pending={create.isPending} disabled={form.event_types.length === 0}>Create endpoint</SubmitButton></>} wide><Form id="create-webhook" className="form-grid" onSubmit={() => create.mutateAsync()}><label><span>Name *</span><input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="Budget event receiver" /></label><label><span>HTTPS URL *</span><input required type="url" value={form.url} onChange={(event) => setForm({ ...form, url: event.target.value })} placeholder="https://hooks.example.com/relaydock" /></label><label className="full-span"><span>Signing secret *</span><input required type="password" autoComplete="new-password" minLength={16} value={form.secret} onChange={(event) => setForm({ ...form, secret: event.target.value })} placeholder="Use a random secret from your secret manager" /><small>The plaintext secret is never displayed after creation.</small></label><fieldset className="event-options full-span"><legend>Subscribed events *</legend>{eventOptions.map((event) => <label key={event}><input type="checkbox" checked={form.event_types.includes(event)} onChange={(change) => setForm({ ...form, event_types: change.target.checked ? [...form.event_types, event] : form.event_types.filter((value) => value !== event) })} /><span>{event}</span></label>)}</fieldset>{form.event_types.length === 0 && <div className="inline-warning full-span">Select at least one event type.</div>}{create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Unable to create webhook.'}</div>}</Form></Modal>
  </div>
}
