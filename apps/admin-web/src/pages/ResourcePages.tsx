import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, CheckCircle2, Copy, Download, KeyRound, RefreshCw, RotateCcw, Route as RouteIcon, Ticket, Trash2, UserCheck } from 'lucide-react'
import { ResourcePage, cell, type ResourceConfig } from '../components/ResourcePage'
import { Badge, Button, Form, Modal, StatusBadge, SubmitButton, useToast } from '../components/ui'
import { api, apiDownload, asPage, formatDate, formatMoney, formatNumber } from '../lib/api'
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
      { key: 'commercial_status', label: 'Commercial', render: (row) => <StatusBadge value={row.commercial_status} /> },
      { key: 'commercial_resale_status', label: 'Resale', render: (row) => <StatusBadge value={row.commercial_resale_status} /> },
      { key: 'allowed_customer_regions', label: 'Customer regions', render: cell.tags('allowed_customer_regions') },
      { key: 'kill_switch', label: 'Kill switch', render: (row) => <StatusBadge value={row.emergency_kill_switch ? 'STOPPED' : 'READY'} /> },
      { key: 'credentials_count', label: 'Credentials', render: (row) => formatNumber(row.credentials_count ?? row.member_count, false) },
      { key: 'last_health_check_at', label: 'Last check', render: cell.date('last_health_check_at') },
    ],
    createFields: [
      { name: 'name', label: 'Display name', required: true, placeholder: 'OpenAI' },
      { name: 'slug', label: 'Slug', required: true, placeholder: 'openai' },
      { name: 'provider_type', label: 'Provider type', required: true, type: 'select', options: [
        { value: 'openai', label: 'OpenAI' }, { value: 'anthropic', label: 'Anthropic' }, { value: 'gemini', label: 'Google Gemini' },
        { value: 'deepseek', label: 'DeepSeek' }, { value: 'qwen', label: 'Qwen' }, { value: 'kimi', label: 'Kimi' },
        { value: 'glm', label: 'GLM' }, { value: 'openrouter', label: 'OpenRouter' },
      ] },
      { name: 'base_url', label: 'Base URL', type: 'url', required: true, placeholder: 'https://api.openai.com/v1', hint: 'Use the official provider API endpoint.' },
      { name: 'commercial_status', label: 'Commercial status', type: 'select', required: true, options: [{ value: 'CONTRACT_PENDING', label: 'Contract pending' }, { value: 'TECHNICALLY_AVAILABLE', label: 'Technically available only' }, { value: 'COMMERCIAL_APPROVED', label: 'Commercial approved' }, { value: 'SUSPENDED', label: 'Suspended' }, { value: 'EXPIRED', label: 'Expired' }, { value: 'TERMINATED', label: 'Terminated' }] },
      { name: 'legal_entity', label: 'Legal entity', required: true },
      { name: 'contract_type', label: 'Contract type', required: true, placeholder: 'DIRECT_API_RESALE' },
      { name: 'commercial_resale_status', label: 'Resale status', type: 'select', required: true, options: [{value:'NOT_APPROVED',label:'Not approved'},{value:'PENDING',label:'Pending'},{value:'APPROVED',label:'Approved'},{value:'PROHIBITED',label:'Prohibited'}] },
      { name: 'contract_start_at', label: 'Contract start', placeholder: '2026-08-13T00:00:00Z' },
      { name: 'contract_end_at', label: 'Contract end', placeholder: '2027-08-13T00:00:00Z' },
      { name: 'allowed_customer_regions', label: 'Allowed customer regions', required: true, placeholder: 'CN, SG, US or *' },
      { name: 'prohibited_regions', label: 'Prohibited regions', placeholder: 'Comma-separated regions' },
      { name: 'data_processing_regions', label: 'Data processing regions', required: true, placeholder: 'CN, SG' },
      { name: 'data_retention_policy', label: 'Data retention policy', required: true },
      { name: 'terms_version', label: 'Provider terms version', required: true },
      { name: 'cost_limit', label: 'Monthly cost limit (exact decimal)', placeholder: '1000.000000000000' },
      { name: 'rate_limit', label: 'Aggregate RPM', type: 'number' },
      { name: 'settlement_currency', label: 'Settlement currency', required: true, placeholder: 'USD' },
      { name: 'emergency_kill_switch', label: 'Emergency kill switch', type: 'select', required: true, options: [{value:'false',label:'Ready'},{value:'true',label:'Stop new traffic'}] },
      { name: 'pricing_disabled', label: 'Pricing switch', type: 'select', required: true, options: [{ value: 'false', label: 'Enabled' }, { value: 'true', label: 'Disabled' }] },
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
      { key: 'quality_score', label: 'Quality', render: (row) => formatNumber(row.quality_score, false) },
      { key: 'latency_score', label: 'Latency penalty', render: (row) => formatNumber(row.latency_score, false) },
      { key: 'input_price', label: 'Input / 1M', render: (row) => formatMoney(row.input_price) },
      { key: 'output_price', label: 'Output / 1M', render: (row) => formatMoney(row.output_price) },
	  { key: 'service_subject', label: 'Service subject' },
	  { key: 'generated_content_label_capability', label: 'Content label', render: (row) => <StatusBadge value={String(row.generated_content_label_capability || 'UNKNOWN')} /> },
    ],
    createFields: [
      { name: 'provider_id', label: 'Provider ID', required: true },
      { name: 'provider_model_id', label: 'Provider model ID', required: true, placeholder: 'deepseek-chat' },
      { name: 'display_name', label: 'Display name', required: true, placeholder: 'DeepSeek Chat' },
      { name: 'model_type', label: 'Type', type: 'select', required: true, options: [{ value: 'text', label: 'Text' }, { value: 'embedding', label: 'Embedding' }, { value: 'image', label: 'Image' }, { value: 'audio', label: 'Audio' }] },
      { name: 'context_window', label: 'Context window', type: 'number' },
      { name: 'quality_score', label: 'Quality score (0–100)', type: 'number', placeholder: '50' },
      { name: 'latency_score', label: 'Latency penalty (0–100)', type: 'number', placeholder: '50' },
      { name: 'input_price', label: 'Input price / 1M', type: 'number', placeholder: '0' },
      { name: 'output_price', label: 'Output price / 1M', type: 'number', placeholder: '0' },
	  { name: 'service_subject', label: 'Model service subject (legal review required)' },
	  { name: 'filing_info', label: 'Filing / registration reference (legal review required)' },
	  { name: 'generated_content_label_capability', label: 'Generated content labeling', type: 'select', options: [{ value: 'UNKNOWN', label: 'Unknown' }, { value: 'SUPPORTED', label: 'Supported' }, { value: 'UNSUPPORTED', label: 'Unsupported' }] },
	  { name: 'user_disclosure', label: 'User-visible disclosure (legal review required)', type: 'textarea' },
    ],
    emptyTitle: 'Model registry is empty', emptyDescription: 'Connect a provider, then sync its official model catalog.',
  }} />
}

export function RoutingRulesPage() {
  return <ResourcePage config={{
    title: 'Routing rules', description: 'Select models by cost, quality, or a weighted quality − price − latency score.', endpoint: '/routing-rules', noun: 'Routing rule', createLabel: 'Create rule', filters: [{ value: 'enabled', label: 'Enabled' }, { value: 'disabled', label: 'Disabled' }], sort: commonSort,
    columns: [
      { key: 'name', label: 'Rule', render: cell.primary('name', 'alias') },
      { key: 'project_id', label: 'Project', render: (row) => <code className="inline-code">{String(row.project_id || '—')}</code> },
      { key: 'strategy', label: 'Strategy', render: (row) => <Badge tone="info">{String(row.strategy || 'balanced')}</Badge> },
      { key: 'quality_weight', label: 'Quality weight', render: (row) => formatNumber(row.quality_weight, false) },
      { key: 'price_weight', label: 'Price weight', render: (row) => formatNumber(row.price_weight, false) },
      { key: 'latency_weight', label: 'Latency weight', render: (row) => formatNumber(row.latency_weight, false) },
      { key: 'enabled', label: 'Status', render: (row) => <StatusBadge value={row.enabled ? 'enabled' : 'disabled'} /> },
    ],
    createFields: [
      { name: 'project_id', label: 'Project ID', required: true },
      { name: 'name', label: 'Rule name', required: true, placeholder: 'Balanced production' },
      { name: 'alias', label: 'API model alias', required: true, placeholder: 'auto-production' },
      { name: 'strategy', label: 'Strategy', required: true, type: 'select', options: [{ value: 'balanced', label: 'Balanced' }, { value: 'cost_optimized', label: 'Cost optimized' }, { value: 'quality_optimized', label: 'Quality optimized' }] },
      { name: 'quality_weight', label: 'Quality weight', type: 'number', placeholder: '0.5' },
      { name: 'price_weight', label: 'Price weight', type: 'number', placeholder: '0.3' },
      { name: 'latency_weight', label: 'Latency weight', type: 'number', placeholder: '0.2' },
    ],
  }} />
}

export function MarketplacePage() {
  return <ResourcePage config={{
    title: 'Provider marketplace', description: 'Review third-party provider endpoints, supported models, verification, pricing, and uptime.', endpoint: '/marketplace/providers', noun: 'Marketplace provider', filters: [{ value: 'ACTIVE', label: 'Active' }, { value: 'REVIEW', label: 'In review' }, { value: 'SUSPENDED', label: 'Suspended' }], sort: commonSort,
    columns: [
      { key: 'provider_name', label: 'Provider', render: cell.primary('provider_name', 'endpoint') },
      { key: 'supported_models', label: 'Models', render: cell.tags('supported_models') },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'verified', label: 'Verified', render: (row) => <StatusBadge value={row.verified ? 'verified' : 'unverified'} /> },
      { key: 'uptime', label: 'Uptime', render: (row) => `${Number(row.uptime || 0).toFixed(3)}%` },
      { key: 'updated_at', label: 'Updated', render: cell.date('updated_at') },
    ],
  }} />
}

export function TeamsPage() {
  return <ResourcePage config={{
    title: 'Teams', description: 'Group organization members and apply commercial token and cost limits.', endpoint: '/teams', noun: 'Team', createLabel: 'Create team', filters: [{ value: 'ACTIVE', label: 'Active' }, { value: 'DISABLED', label: 'Disabled' }, { value: 'ARCHIVED', label: 'Archived' }], sort: commonSort,
    columns: [
      { key: 'name', label: 'Team', render: cell.primary('name', 'slug') },
      { key: 'organization_id', label: 'Organization', render: (row) => <code className="inline-code">{String(row.organization_id || '—')}</code> },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'monthly_token_limit', label: 'Token limit', render: (row) => formatNumber(row.monthly_token_limit) },
      { key: 'monthly_cost_limit', label: 'Cost limit', render: (row) => formatMoney(row.monthly_cost_limit) },
    ],
    createFields: [
      { name: 'organization_id', label: 'Organization ID', required: true },
      { name: 'name', label: 'Team name', required: true, placeholder: 'AI Platform' },
      { name: 'slug', label: 'Slug', placeholder: 'ai-platform' },
      { name: 'status', label: 'Status', type: 'select', required: true, options: [{ value: 'ACTIVE', label: 'Active' }, { value: 'DISABLED', label: 'Disabled' }] },
      { name: 'monthly_token_limit', label: 'Monthly token limit', type: 'number' },
      { name: 'monthly_cost_limit', label: 'Monthly cost limit', type: 'number' },
    ],
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
      { name: 'team_id', label: 'Team ID', hint: 'Optional. The key owner must be an active member of this team.' },
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
    headerExtra: <RegistrationInvitesButton />,
    rowActions: (row) => <UserStatusAction row={row} />,
    createFields: [{ name: 'email', label: 'Email address', type: 'text', required: true }, { name: 'display_name', label: 'Display name', required: true }, { name: 'password', label: 'Temporary password', type: 'password', required: true, hint: 'Share through an approved secure channel.' }, { name: 'role', label: 'Role', type: 'select', required: true, options: [{ value: 'USER', label: 'User' }, { value: 'ADMIN', label: 'Administrator' }] }],
  }} />
}

function UserStatusAction({ row }: { row: Record<string, unknown> }) {
  const client = useQueryClient()
  const toast = useToast()
  const active = String(row.status).toUpperCase() === 'ACTIVE'
  const update = useMutation({
    mutationFn: () => api(`/users/${row.id}/status`, { method: 'PATCH', body: JSON.stringify({ status: active ? 'SUSPENDED' : 'ACTIVE' }) }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['resource', '/users'] }); toast(active ? 'Account suspended' : 'Account restored') },
  })
  return <Button size="sm" variant="ghost" disabled={update.isPending} onClick={() => update.mutate()} title={active ? 'Suspend account' : 'Restore account'}>{active ? <Ban size={14} /> : <UserCheck size={14} />}</Button>
}

type RegistrationInvite = { id: string; status: string; max_uses: number; used_count: number; expires_at: string }

function RegistrationInvitesButton() {
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ max_uses: '1', expires_in_hours: '168' })
  const [code, setCode] = useState('')
  const client = useQueryClient()
  const toast = useToast()
  const invites = useQuery({ queryKey: ['registration-invites'], queryFn: () => api<unknown>('/registration-invites').then(asPage<RegistrationInvite>), enabled: open })
  const create = useMutation({ mutationFn: () => api<{ code: string }>('/registration-invites', { method: 'POST', body: JSON.stringify({ max_uses: Number(form.max_uses), expires_in_hours: Number(form.expires_in_hours) }) }), onSuccess: (result) => { setCode(result.code); void client.invalidateQueries({ queryKey: ['registration-invites'] }) } })
  const revoke = useMutation({ mutationFn: (id: string) => api(`/registration-invites/${id}`, { method: 'DELETE' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['registration-invites'] }); toast('Registration invite revoked') } })
  return <><Button onClick={() => setOpen(true)}><Ticket size={15} />Registration invites</Button><Modal open={open} onClose={() => { setOpen(false); setCode('') }} title="Registration invites" description="Issue bounded one-time or multi-use codes for INVITE_ONLY registration." footer={<Button onClick={() => setOpen(false)}>Close</Button>} wide><Form className="form-grid" onSubmit={() => create.mutateAsync()}><label><span>Maximum uses</span><input required type="number" min="1" max="10000" value={form.max_uses} onChange={(event) => setForm({ ...form, max_uses: event.target.value })} /></label><label><span>Expires in hours</span><input required type="number" min="1" max="8760" value={form.expires_in_hours} onChange={(event) => setForm({ ...form, expires_in_hours: event.target.value })} /></label><SubmitButton pending={create.isPending}>Create code</SubmitButton>{create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Could not create invite.'}</div>}</Form>{code && <div className="secret-box"><code>{code}</code><Button onClick={() => { void navigator.clipboard.writeText(code); toast('Registration code copied') }}><Copy size={13} />Copy</Button></div>}<div className="drawer-section">{invites.data?.items.map((invite) => <div className="member-row" key={invite.id}><Ticket size={15} /><div><strong>{invite.used_count} / {invite.max_uses} used</strong><small>Expires {formatDate(invite.expires_at)}</small></div><StatusBadge value={invite.status} />{invite.status === 'ACTIVE' && <Button size="sm" variant="ghost" onClick={() => revoke.mutate(invite.id)} aria-label="Revoke registration invite"><Trash2 size={13} /></Button>}</div>)}</div></Modal></>
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
