import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Copy, Download, KeyRound, RefreshCw, RotateCcw, Route as RouteIcon } from 'lucide-react'
import { ResourcePage, cell, type ResourceConfig } from '../components/ResourcePage'
import { Badge, Button, Form, Modal, StatusBadge, SubmitButton, useToast } from '../components/ui'
import { api, apiDownload, formatMoney, formatNumber } from '../lib/api'
import { useAdminTenantScope, v2Paths } from '../lib/v2'
import { TenantSelector } from '../components/TenantSelector'

const commonSort = [
  { value: '-created_at', label: 'Newest first' },
  { value: 'created_at', label: 'Oldest first' },
  { value: 'name', label: 'Name A–Z' },
]

const statusFilters = [
  { value: 'ACTIVE', label: 'Active' },
  { value: 'DISABLED', label: 'Disabled' },
  { value: 'UNHEALTHY', label: 'Unhealthy' },
]

export function ProvidersPage() {
  const config: ResourceConfig = {
    title: 'Providers',
    description: 'Configure official upstream API providers and connection health.',
    endpoint: '/providers', noun: 'Provider', createLabel: 'Add provider', filters: statusFilters, sort: commonSort,
    columns: [
      { key: 'name', label: 'Provider', render: cell.primary('name', 'provider_type') },
      { key: 'base_url', label: 'Base URL', className: 'wide-cell', render: (row) => <code className="inline-code">{String(row.base_url || '—')}</code> },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.enabled === false ? 'disabled' : row.status || 'enabled'} /> },
      { key: 'credentials_count', label: 'Credentials', render: (row) => formatNumber(row.credentials_count ?? row.member_count, false) },
      { key: 'last_health_check_at', label: 'Last check', render: cell.date('last_health_check_at') },
    ],
    createFields: [
      { name: 'name', label: 'Display name', required: true, placeholder: 'OpenAI' },
      { name: 'slug', label: 'Slug', required: true, placeholder: 'openai' },
      { name: 'provider_type', label: 'Provider type', required: true, type: 'select', options: [{ value: 'openai', label: 'OpenAI' }, { value: 'deepseek', label: 'DeepSeek' }, { value: 'openrouter', label: 'OpenRouter' }] },
      { name: 'base_url', label: 'Base URL', type: 'url', required: true, placeholder: 'https://api.openai.com/v1', hint: 'Use the official provider API endpoint.' },
    ],
    emptyTitle: 'No providers configured', emptyDescription: 'Add an official provider connection to begin configuring authorized credentials.',
  }
  return <ResourcePage config={config} />
}

export function GroupsPage() {
  return <ResourcePage config={{
    title: 'Credential groups', description: 'Organize credentials into deterministic scheduling pools.', endpoint: '/credential-groups', noun: 'Group', createLabel: 'Create group', sort: commonSort,
    columns: [
      { key: 'name', label: 'Group', render: cell.primary('name', 'description') },
      { key: 'credentials_count', label: 'Credentials', render: (row) => formatNumber(row.credentials_count ?? row.member_count, false) },
      { key: 'healthy_count', label: 'Healthy', render: (row) => <span className="health-value">{formatNumber(row.healthy_count, false)}</span> },
      { key: 'capacity', label: 'Capacity', render: (row) => formatNumber(row.total_capacity ?? row.capacity, false) },
      { key: 'recent_requests', label: 'Recent requests', render: (row) => formatNumber(row.recent_requests) },
      { key: 'updated_at', label: 'Updated', render: cell.date('updated_at') },
    ],
    createFields: [{ name: 'name', label: 'Group name', required: true, placeholder: 'Production Pool' }, { name: 'description', label: 'Description', type: 'textarea', placeholder: 'What traffic should this pool serve?' }],
  }} />
}

export function ModelsPage() {
  return <ResourcePage config={{
    title: 'Models', description: 'Manage the provider model registry, capabilities, and configured pricing.', endpoint: '/models', noun: 'Model', filters: [{ value: 'enabled', label: 'Enabled' }, { value: 'disabled', label: 'Disabled' }], sort: commonSort,
    headerExtra: <SyncModelsButton />,
    columns: [
      { key: 'display_name', label: 'Model', render: cell.primary('display_name', 'provider_model_id') },
      { key: 'provider_name', label: 'Provider' },
      { key: 'model_type', label: 'Type', render: (row) => <Badge>{String(row.model_type || 'unknown')}</Badge> },
      { key: 'capabilities', label: 'Capabilities', render: cell.tags('capabilities') },
      { key: 'enabled', label: 'Status', render: (row) => <StatusBadge value={row.enabled ? 'enabled' : 'disabled'} /> },
      { key: 'input_price', label: 'Input / 1M', render: (row) => formatMoney(row.input_price) },
      { key: 'output_price', label: 'Output / 1M', render: (row) => formatMoney(row.output_price) },
    ],
    emptyTitle: 'Model registry is empty', emptyDescription: 'Connect a provider, then sync its official model catalog.',
  }} />
}

export function RoutesPage() {
  return <ResourcePage config={{
    title: 'Model routes', description: 'Map stable RelayDock aliases to provider models and credential pools.', endpoint: '/model-routes', noun: 'Route', createLabel: 'Create route', filters: [{ value: 'enabled', label: 'Enabled' }, { value: 'disabled', label: 'Disabled' }], sort: commonSort,
    columns: [
      { key: 'alias', label: 'Alias', render: (row) => <div className="route-alias"><RouteIcon size={14} /><code>{String(row.alias || '—')}</code></div> },
      { key: 'upstream_model', label: 'Upstream model', render: cell.primary('upstream_model', 'provider_name') },
      { key: 'credential_group_name', label: 'Primary pool' },
      { key: 'fallback_group_name', label: 'Fallback pool' },
      { key: 'routing_policy', label: 'Policy', render: (row) => <Badge tone="info">{String(row.routing_policy || 'priority_weighted')}</Badge> },
      { key: 'enabled', label: 'Status', render: (row) => <StatusBadge value={row.enabled ? 'enabled' : 'disabled'} /> },
    ],
    createFields: [
      { name: 'alias', label: 'RelayDock alias', required: true, placeholder: 'gpt-default' },
      { name: 'provider_id', label: 'Provider ID', required: true },
      { name: 'upstream_model', label: 'Upstream model ID', required: true },
      { name: 'credential_group_id', label: 'Primary credential group ID', required: true },
      { name: 'fallback_group_id', label: 'Fallback group ID', hint: 'Used only when safe failover criteria are met.' },
      { name: 'routing_policy', label: 'Routing policy', type: 'select', required: true, options: [{ value: 'priority_weighted', label: 'Priority weighted' }, { value: 'least_loaded', label: 'Least loaded' }, { value: 'weighted_round_robin', label: 'Weighted round robin' }] },
    ],
    emptyTitle: 'No model routes', emptyDescription: 'Create an alias after models and credential groups are configured.',
  }} />
}

export function AdminApiKeysPage() {
  return <ResourcePage config={{
    title: 'API keys', description: 'Manage project-scoped downstream keys, versioned rotation, quotas, and rate limits.', endpoint: '/api-keys', noun: 'API key', createLabel: 'Create API key', filters: [{ value: 'ACTIVE', label: 'Active' }, { value: 'DISABLED', label: 'Disabled' }, { value: 'REVOKED', label: 'Revoked' }], sort: commonSort,
    columns: [
      { key: 'name', label: 'Key', render: (row) => <div className="primary-cell"><strong>{String(row.name || 'Unnamed key')}</strong><code>{String(row.key_prefix || 'rdk_••••')}</code></div> },
      { key: 'user_email', label: 'Owner' },
      { key: 'project_id', label: 'Project', render: (row) => <code className="inline-code">{String(row.project_id || 'legacy')}</code> },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'rate_limit_rpm', label: 'RPM', render: (row) => formatNumber(row.rate_limit_rpm, false) },
      { key: 'monthly_token_limit', label: 'Monthly tokens', render: (row) => formatNumber(row.monthly_token_limit) },
      { key: 'last_used_at', label: 'Last used', render: cell.date('last_used_at') },
    ],
    createFields: [
      { name: 'name', label: 'Key name', required: true, placeholder: 'Production service' },
      { name: 'user_id', label: 'User ID', required: true },
      { name: 'project_id', label: 'Project ID', required: true, hint: 'The owner must have active access to this project.' },
      { name: 'environment', label: 'Environment', type: 'select', required: true, options: [{ value: 'live', label: 'Live' }, { value: 'test', label: 'Test' }] },
      { name: 'rate_limit_rpm', label: 'Requests per minute', type: 'number', placeholder: '600' },
      { name: 'rate_limit_tpm', label: 'Tokens per minute', type: 'number', placeholder: '200000' },
      { name: 'monthly_token_limit', label: 'Monthly token limit', type: 'number' },
      { name: 'monthly_cost_limit', label: 'Monthly cost limit (USD)', type: 'number' },
    ],
    rowActions: (row) => <AdminKeyLifecycle row={row} />,
  }} />
}

function AdminKeyLifecycle({ row }: { row: Record<string, unknown> }) {
  const [rotateOpen, setRotateOpen] = useState(false)
  const [finalizeOpen, setFinalizeOpen] = useState(false)
  const [graceMinutes, setGraceMinutes] = useState('60')
  const [secret, setSecret] = useState('')
  const client = useQueryClient()
  const toast = useToast()
  const keyID = String(row.id || '')
  const rotate = useMutation({
    mutationFn: () => api<Record<string, unknown>>(v2Paths.apiKeyRotate(String(row.project_id || ''), keyID), { method: 'POST', body: JSON.stringify({ grace_seconds: Number(graceMinutes) * 60 }) }),
    onSuccess: (value) => { setRotateOpen(false); setSecret(String(value.secret || value.key || '')); void client.invalidateQueries({ queryKey: ['resource', '/api-keys'] }); toast('API key rotated') },
  })
  const finalize = useMutation({
    mutationFn: () => api(v2Paths.apiKeyFinalize(String(row.project_id || ''), keyID), { method: 'POST', body: JSON.stringify({ version: 0 }) }),
    onSuccess: () => { setFinalizeOpen(false); void client.invalidateQueries({ queryKey: ['resource', '/api-keys'] }); toast('Rotation finalized') },
  })
  const active = String(row.status).toUpperCase() === 'ACTIVE'
  return <><Button size="sm" disabled={!active} onClick={() => setRotateOpen(true)}><RotateCcw size={13} />Rotate</Button><Button size="sm" variant="ghost" disabled={!active} onClick={() => setFinalizeOpen(true)} title="Finalize grace versions"><CheckCircle2 size={13} /></Button><Modal open={rotateOpen} onClose={() => setRotateOpen(false)} title="Rotate project API key" description="The new secret is shown once; the old secret remains valid during the grace window." footer={<><Button onClick={() => setRotateOpen(false)}>Cancel</Button><SubmitButton form={`admin-key-rotate-${keyID}`} pending={rotate.isPending}>Rotate key</SubmitButton></>}><Form id={`admin-key-rotate-${keyID}`} className="form-grid" onSubmit={() => rotate.mutateAsync()}><label className="full-span"><span>Grace period (minutes) *</span><input required type="number" min="1" max="1440" value={graceMinutes} onChange={(event) => setGraceMinutes(event.target.value)} /></label>{rotate.isError && <div className="form-error full-span">{rotate.error instanceof Error ? rotate.error.message : 'Rotation failed.'}</div>}</Form></Modal><Modal open={finalizeOpen} onClose={() => setFinalizeOpen(false)} title="Finalize key rotation" description="Immediately revoke every older grace version." footer={<><Button onClick={() => setFinalizeOpen(false)}>Cancel</Button><Button variant="danger" disabled={finalize.isPending} onClick={() => finalize.mutate()}>{finalize.isPending ? 'Finalizing…' : 'Finalize'}</Button></>}><div className="inline-warning">Confirm all workloads use the newest secret. Older secrets cannot be restored.</div>{finalize.isError && <div className="form-error">{finalize.error instanceof Error ? finalize.error.message : 'Finalization failed.'}</div>}</Modal><Modal open={Boolean(secret)} onClose={() => setSecret('')} title="Save the rotated API key" description="This value will not be displayed again."><div className="one-time-label"><span><KeyRound size={14} />ONE-TIME SECRET</span></div><div className="secret-box"><code>{secret}</code><Button onClick={() => { void navigator.clipboard.writeText(secret); toast('API key copied') }}><Copy size={13} />Copy</Button></div><div className="inline-warning">Store this secret in an approved secret manager before closing.</div></Modal></>
}

export function UsersPage() {
  return <ResourcePage config={{
    title: 'Users', description: 'Control user roles, access status, quotas, and issued keys.', endpoint: '/users', noun: 'User', createLabel: 'Invite user', filters: [{ value: 'ACTIVE', label: 'Active' }, { value: 'SUSPENDED', label: 'Suspended' }], sort: commonSort,
    columns: [
      { key: 'email', label: 'User', render: cell.primary('display_name', 'email') },
      { key: 'role', label: 'Role', render: (row) => <Badge tone={row.role === 'SUPER_ADMIN' ? 'violet' : 'neutral'}>{String(row.role || 'USER')}</Badge> },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'api_keys_count', label: 'Keys', render: (row) => formatNumber(row.api_keys_count, false) },
      { key: 'usage_tokens', label: 'Usage', render: (row) => formatNumber(row.usage_tokens) },
      { key: 'last_login_at', label: 'Last login', render: cell.date('last_login_at') },
    ],
    createFields: [{ name: 'email', label: 'Email address', type: 'text', required: true }, { name: 'display_name', label: 'Display name', required: true }, { name: 'password', label: 'Temporary password', type: 'password', required: true, hint: 'Share through an approved secure channel.' }, { name: 'role', label: 'Role', type: 'select', required: true, options: [{ value: 'USER', label: 'User' }, { value: 'ADMIN', label: 'Administrator' }] }],
  }} />
}

export function UsagePage() {
  return <ResourcePage config={{
    title: 'Usage', description: 'Analyze requests, tokens, errors, latency, and configured cost accounting.', endpoint: '/usage', noun: 'Usage record', periodFilter: true,
    headerExtra: <ProjectUsageExportButton />,
    columns: [
      { key: 'date', label: 'Period' },
      { key: 'user_email', label: 'User' },
      { key: 'model', label: 'Model', render: (row) => <code className="inline-code">{String(row.model || '—')}</code> },
      { key: 'requests', label: 'Requests', render: (row) => formatNumber(row.requests) },
      { key: 'input_tokens', label: 'Input', render: (row) => formatNumber(row.input_tokens) },
      { key: 'cached_input_tokens', label: 'Cached', render: (row) => formatNumber(row.cached_input_tokens) },
      { key: 'output_tokens', label: 'Output', render: (row) => formatNumber(row.output_tokens) },
      { key: 'cost', label: 'Est. cost', render: (row) => <strong>{formatMoney(row.cost)}</strong> },
      { key: 'errors', label: 'Errors', render: (row) => formatNumber(row.errors, false) },
    ],
  }} />
}

function ProjectUsageExportButton() {
  const scope = useAdminTenantScope()
  const [open, setOpen] = useState(false)
  const today = new Date().toISOString().slice(0, 10)
  const [from, setFrom] = useState(() => new Date(Date.now() - 29 * 86400000).toISOString().slice(0, 10))
  const [to, setTo] = useState(today)
  const toast = useToast()
  const download = useMutation({
    mutationFn: () => apiDownload(v2Paths.projectUsageExport(scope.projectID), `relaydock-${scope.project?.slug || 'project'}-usage-${from}-${to}.csv`, { from, to }),
    onSuccess: () => { setOpen(false); toast('Project usage CSV downloaded') },
  })
  return <><Button onClick={() => setOpen(true)}><Download size={14} />Project CSV</Button><Modal open={open} onClose={() => setOpen(false)} title="Export project usage" description="CSV rows contain bounded project metadata and accounting fields, never prompts, responses, or secrets." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><Button variant="primary" disabled={!scope.projectID || !from || !to || download.isPending} onClick={() => download.mutate()}>{download.isPending ? 'Exporting…' : 'Download CSV'}</Button></>} wide><div className="form-grid"><div className="full-span"><TenantSelector organizations={scope.organizationRows} organizationID={scope.organizationID} onOrganizationChange={scope.setOrganizationID} projects={scope.projectRows} projectID={scope.projectID} onProjectChange={scope.setProjectID} projectRequired /></div><label><span>From (UTC) *</span><input type="date" required max={to} value={from} onChange={(event) => setFrom(event.target.value)} /></label><label><span>To (UTC) *</span><input type="date" required min={from} max={today} value={to} onChange={(event) => setTo(event.target.value)} /></label>{download.isError && <div className="form-error full-span">{download.error instanceof Error ? download.error.message : 'Unable to export project usage.'}</div>}</div></Modal></>
}

export function RequestLogsPage() {
  return <ResourcePage config={{
    title: 'Request logs', description: 'Inspect gateway decisions and sanitized request metadata. Prompt content is never shown.', endpoint: '/request-logs', noun: 'Request log', filters: [{ value: 'success', label: 'Success' }, { value: 'error', label: 'Error' }, { value: '429', label: 'Rate limited' }], sort: [{ value: '-created_at', label: 'Newest first' }, { value: '-latency_ms', label: 'Slowest first' }],
    columns: [
      { key: 'created_at', label: 'Time', render: cell.date('created_at') },
      { key: 'request_id', label: 'Request ID', render: (row) => <code className="inline-code">{String(row.request_id || '—')}</code> },
      { key: 'user_email', label: 'User' },
      { key: 'requested_model', label: 'Requested model', render: cell.primary('requested_model', 'resolved_model') },
      { key: 'credential_name', label: 'Credential' },
      { key: 'status_code', label: 'Status', render: (row) => <StatusBadge value={String(row.status_code || 'unknown')} /> },
      { key: 'total_tokens', label: 'Tokens', render: (row) => formatNumber(row.total_tokens) },
      { key: 'latency_ms', label: 'Latency', render: (row) => row.latency_ms === undefined ? '—' : `${formatNumber(row.latency_ms, false)} ms` },
      { key: 'ttft_ms', label: 'TTFT', render: (row) => row.ttft_ms === undefined ? '—' : `${formatNumber(row.ttft_ms, false)} ms` },
    ],
  }} />
}

export function AuditLogsPage() {
  return <ResourcePage config={{
    title: 'Audit logs', description: 'Immutable history of administrative and security-sensitive operations.', endpoint: '/audit-logs', noun: 'Audit event', filters: [{ value: 'credential', label: 'Credential' }, { value: 'api_key', label: 'API key' }, { value: 'user', label: 'User' }], sort: [{ value: '-timestamp', label: 'Newest first' }, { value: 'timestamp', label: 'Oldest first' }],
    columns: [
      { key: 'timestamp', label: 'Time', render: cell.date('timestamp') },
      { key: 'actor', label: 'Actor', render: cell.primary('actor', 'ip') },
      { key: 'action', label: 'Action', render: (row) => <Badge tone="info">{String(row.action || '—')}</Badge> },
      { key: 'resource_type', label: 'Resource', render: cell.primary('resource_type', 'resource_id') },
      { key: 'result', label: 'Result', render: (row) => <StatusBadge value={row.result || 'success'} /> },
    ],
  }} />
}

export function AlertsPage() {
  return <ResourcePage config={{
    title: 'Alerts', description: 'Review credential, pool, error-rate, and quota threshold conditions.', endpoint: '/alerts', noun: 'Alert', filters: [{ value: 'OPEN', label: 'Open' }, { value: 'ACKNOWLEDGED', label: 'Acknowledged' }, { value: 'RESOLVED', label: 'Resolved' }], sort: [{ value: '-created_at', label: 'Newest first' }, { value: '-severity', label: 'Severity' }],
    columns: [
      { key: 'title', label: 'Alert', render: cell.primary('title', 'message') },
      { key: 'severity', label: 'Severity', render: (row) => <StatusBadge value={row.severity} /> },
      { key: 'resource_type', label: 'Resource' },
      { key: 'status', label: 'State', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'created_at', label: 'Opened', render: cell.date('created_at') },
    ],
    rowActions: (row) => <AcknowledgeAlertButton row={row} />,
  }} />
}

function AcknowledgeAlertButton({ row }: { row: Record<string, unknown> }) {
  const client = useQueryClient()
  const toast = useToast()
  const acknowledge = useMutation({
    mutationFn: () => api(v2Paths.alertAcknowledge(String(row.id)), { method: 'POST' }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['resource', '/alerts'] }); toast('Alert acknowledged') },
  })
  const acknowledged = Boolean(row.acknowledged_at) || String(row.status).toUpperCase() === 'ACKNOWLEDGED'
  return <Button size="sm" disabled={acknowledged || acknowledge.isPending} onClick={() => acknowledge.mutate()} title={acknowledge.isError ? (acknowledge.error instanceof Error ? acknowledge.error.message : 'Acknowledgement failed') : undefined}><CheckCircle2 size={13} />{acknowledged ? 'Acknowledged' : 'Acknowledge'}</Button>
}

export function NotFoundPage() {
  return <div className="not-found"><span>404</span><h1>Page not found</h1><p>The requested RelayDock admin page does not exist.</p><a href="/"><Button variant="primary">Return to dashboard</Button></a></div>
}

function SyncModelsButton() {
  const [open, setOpen] = useState(false)
  const [providerId, setProviderId] = useState('')
  const [credentialId, setCredentialId] = useState('')
  const client = useQueryClient()
  const toast = useToast()
  const sync = useMutation({
    mutationFn: () => api<{ synced?: number }>(`/providers/${providerId}/sync-models`, { method: 'POST', body: JSON.stringify({ credential_id: credentialId }) }),
    onSuccess: (value) => { setOpen(false); void client.invalidateQueries({ queryKey: ['resource', '/models'] }); toast(`${formatNumber(value.synced, false)} models synchronized`) },
  })
  return <><Button onClick={() => setOpen(true)}><RefreshCw size={14} />Sync models</Button><Modal open={open} onClose={() => setOpen(false)} title="Sync provider models" description="RelayDock uses the selected authorized credential to query the provider's official Models API." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="sync-models-form" pending={sync.isPending}>Sync models</SubmitButton></>}><Form id="sync-models-form" className="form-grid" onSubmit={() => sync.mutateAsync()}><label className="full-span"><span>Provider ID *</span><input required value={providerId} onChange={(event) => setProviderId(event.target.value)} placeholder="Provider ID" /></label><label className="full-span"><span>Credential ID *</span><input required value={credentialId} onChange={(event) => setCredentialId(event.target.value)} placeholder="Authorized credential ID" /></label>{sync.isError && <div className="form-error full-span">{sync.error instanceof Error ? sync.error.message : 'Model synchronization failed.'}</div>}</Form></Modal></>
}
