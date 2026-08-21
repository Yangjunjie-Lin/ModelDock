import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, ArrowUpRight, Boxes, CheckCircle2, Download, Gauge, Grid2X2, KeyRound, List, MoreHorizontal, Plus, RefreshCw, ShieldCheck, TimerReset, Trash2, Upload } from 'lucide-react'
import { api, asPage, formatDate, formatNumber } from '../lib/api'
import type { Credential } from '../lib/types'
import { Badge, Button, Drawer, EmptyState, ErrorState, Form, Modal, Pagination, SearchInput, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type CockpitAccount = {
  id: string
  email_masked: string
  plan: string
  auth_kind: string
  status: 'ready' | 'quota_exhausted' | string
  remaining_quota: number
  remaining_percent: number
  secondary_percent: number
  reset_at?: string
  subscription_expires_at?: string
  updated_at?: string
}

type CockpitTestResult = { ok: boolean; model: string; latency_ms: number; tested_at: string; message: string }
type CockpitPool = { configured: boolean; test_configured: boolean; source: string; generated_at?: string; accounts: CockpitAccount[]; last_test?: CockpitTestResult }

export function CredentialsPage() {
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState('')
  const [group, setGroup] = useState('')
  const [page, setPage] = useState(1)
  const [view, setView] = useState<'cards' | 'table'>('cards')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [addOpen, setAddOpen] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const [importText, setImportText] = useState('')
  const [moveGroupOpen, setMoveGroupOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [moveGroupId, setMoveGroupId] = useState('')
  const [detail, setDetail] = useState<Credential | null>(null)
  const [form, setForm] = useState({ provider_id: '', name: '', secret: '', organization_id: '', project_id: '', group_id: '', tags: '', weight: '100', priority: '10', max_concurrency: '10' })
  const queryClient = useQueryClient()
  const toast = useToast()
  const result = useQuery({
    queryKey: ['credentials', search, status, group, page],
    queryFn: () => api<unknown>('/credentials', { query: { search, status, group, limit: 24, offset: (page - 1) * 24 } }).then(asPage<Credential>),
  })
  const cockpitResult = useQuery({
    queryKey: ['cockpit-accounts'],
    queryFn: () => api<CockpitPool>('/cockpit/accounts'),
    staleTime: 30_000,
  })
  const rows = useMemo(() => result.data?.items || [], [result.data])
  const stats = useMemo(() => ({ active: rows.filter((row) => String(row.status).toUpperCase() === 'ACTIVE').length, healthy: rows.filter((row) => String(row.current_health || row.health).toLowerCase() === 'healthy').length, limited: rows.filter((row) => String(row.status).includes('LIMIT')).length, concurrency: rows.reduce((sum, row) => sum + Number(row.max_concurrency || 0), 0) }), [rows])
  const save = useMutation({
    mutationFn: async (mode: 'validate' | 'disabled') => {
      const credential = await api<Credential>('/credentials', { method: 'POST', body: JSON.stringify({ ...form, credential_type: 'api_key', weight: Number(form.weight), priority: Number(form.priority), max_concurrency: Number(form.max_concurrency), validate: true, save_disabled: mode === 'disabled' }) })
      const tags = splitTags(form.tags)
      if (credential.id && tags.length) await api(`/credentials/${credential.id}/tags`, { method: 'PUT', body: JSON.stringify({ tags }) })
      return credential
    },
    onSuccess: (_, mode) => { setAddOpen(false); setForm({ provider_id: '', name: '', secret: '', organization_id: '', project_id: '', group_id: '', tags: '', weight: '100', priority: '10', max_concurrency: '10' }); void queryClient.invalidateQueries({ queryKey: ['credentials'] }); toast(mode === 'validate' ? 'Credential validated and saved' : 'Credential saved disabled') },
  })
  const importCredentials = useMutation({
    mutationFn: () => {
      const parsed: unknown = JSON.parse(importText)
      const credentials = Array.isArray(parsed) ? parsed : (parsed as { credentials?: unknown })?.credentials
      if (!Array.isArray(credentials) || credentials.length < 1 || credentials.length > 25) throw new Error('Provide a JSON array containing 1 to 25 authorized API credentials.')
      return api<{ created: number; total: number }>('/credentials/import', { method: 'POST', body: JSON.stringify({ credentials, validate: true }) })
    },
    onSuccess: (value) => {
      setImportOpen(false); setImportText(''); void queryClient.invalidateQueries({ queryKey: ['credentials'] })
      toast(`${value.created} of ${value.total} authorized credentials imported`)
    },
  })
  const bulk = useMutation({
    mutationFn: (action: string) => Promise.all([...selected].map((credentialId) => {
      if (action === 'health_check') return api(`/credentials/${credentialId}/test`, { method: 'POST' })
      if (action === 'delete') return api(`/credentials/${credentialId}`, { method: 'DELETE' })
      if (action.startsWith('move:')) return api(`/credential-groups/${action.slice(5)}/members/${credentialId}`, { method: 'PUT', body: JSON.stringify({ weight: 100, priority: 0 }) })
      return api(`/credentials/${credentialId}/status`, { method: 'PATCH', body: JSON.stringify({ status: action === 'enable' ? 'ACTIVE' : 'DISABLED' }) })
    })),
    onSuccess: (_, action) => { setSelected(new Set()); setMoveGroupOpen(false); setDeleteOpen(false); setMoveGroupId(''); void queryClient.invalidateQueries({ queryKey: ['credentials'] }); toast(`${selected.size} credentials updated: ${action.startsWith('move:') ? 'move group' : action}`) },
    onError: (error) => toast(error instanceof Error ? error.message : 'Bulk operation failed', 'danger'),
  })
  const testCredential = useMutation({
    mutationFn: (id: string) => api(`/credentials/${id}/test`, { method: 'POST' }),
    onSuccess: () => toast('Credential health check passed'),
    onError: (error) => toast(error instanceof Error ? error.message : 'Health check failed', 'danger'),
  })
  const refreshCockpit = useMutation({
    mutationFn: () => api<CockpitPool>('/cockpit/refresh', { method: 'POST' }),
    onSuccess: (value) => { queryClient.setQueryData(['cockpit-accounts'], value); toast('Cockpit quota snapshot refreshed') },
    onError: (error) => toast(error instanceof Error ? error.message : 'Cockpit snapshot refresh failed', 'danger'),
  })
  const testCockpit = useMutation({
    mutationFn: () => api<CockpitTestResult>('/cockpit/test', { method: 'POST' }),
    onSuccess: (value) => { toast(`${value.model} Cockpit sidecar check passed in ${value.latency_ms} ms`); void cockpitResult.refetch() },
    onError: (error) => toast(error instanceof Error ? error.message : 'Cockpit sidecar check failed', 'danger'),
  })

  const select = (id: string, checked: boolean) => setSelected((current) => {
    const next = new Set(current)
    if (checked) next.add(id)
    else next.delete(id)
    return next
  })
  const exportMetadata = () => {
    const safeRows = rows.map((row) => ({ name: row.name, provider: row.provider_name || providerName(row.provider), project: row.project_id, status: row.status, group: row.group_name, weight: row.weight }))
    const blob = new Blob([JSON.stringify(safeRows, null, 2)], { type: 'application/json' }); const link = document.createElement('a'); link.href = URL.createObjectURL(blob); link.download = 'relaydock-credential-metadata.json'; link.click(); URL.revokeObjectURL(link.href)
  }

  return (
    <div className="page-stack">
      <div className="page-header"><div><div className="eyebrow-row"><span className="live-dot" />AUTHORIZED CREDENTIALS</div><h1>Credential pool</h1><p>Health, load, and scheduling controls for administrator-supplied provider credentials.</p></div><div className="header-actions"><Button onClick={exportMetadata}><Download size={14} />Export metadata</Button><Button onClick={() => setImportOpen(true)}><Upload size={14} />Import JSON</Button><a className="button button-default button-md" href="https://platform.openai.com/api-keys" target="_blank" rel="noreferrer">Open official dashboard<ArrowUpRight size={14} /></a><Button variant="primary" onClick={() => setAddOpen(true)}><Plus size={15} />Add credential</Button></div></div>
      <div className="credential-stats"><div><span><KeyRound size={15} />Visible credentials</span><strong>{result.isLoading ? '—' : formatNumber(result.data?.total, false)}</strong></div><div><span><ShieldCheck size={15} />Healthy</span><strong>{stats.healthy}</strong></div><div><span><Activity size={15} />Active</span><strong>{stats.active}</strong></div><div><span><TimerReset size={15} />Rate limited</span><strong>{stats.limited}</strong></div><div><span><Gauge size={15} />Max concurrency</span><strong>{formatNumber(stats.concurrency, false)}</strong></div></div>
      <CockpitPanel pool={cockpitResult.data} loading={cockpitResult.isLoading} error={cockpitResult.isError ? cockpitResult.error : undefined} refreshing={refreshCockpit.isPending} testing={testCockpit.isPending} onRefresh={() => refreshCockpit.mutate()} onTest={() => testCockpit.mutate()} onRetry={() => cockpitResult.refetch()} />
      <section className="resource-panel credential-panel">
        <div className="resource-toolbar"><SearchInput value={search} onChange={setSearch} placeholder="Search name, project, or tag…" /><div className="toolbar-controls"><label className="select-control"><select value={status} onChange={(event) => setStatus(event.target.value)}><option value="">All statuses</option><option value="ACTIVE">Active</option><option value="RATE_LIMITED">Rate limited</option><option value="COOLDOWN">Cooldown</option><option value="AUTH_FAILED">Auth failed</option><option value="DISABLED">Disabled</option></select></label><label className="select-control"><Boxes size={14} /><select value={group} onChange={(event) => setGroup(event.target.value)}><option value="">All groups</option><option value="production">Production Pool</option><option value="reasoning">Reasoning Pool</option><option value="embedding">Embedding Pool</option></select></label><div className="view-toggle"><button className={view === 'cards' ? 'active' : ''} onClick={() => setView('cards')} aria-label="Card view"><Grid2X2 size={15} /></button><button className={view === 'table' ? 'active' : ''} onClick={() => setView('table')} aria-label="Table view"><List size={16} /></button></div></div></div>
        {selected.size > 0 && <div className="bulk-bar"><span><strong>{selected.size}</strong> selected</span><Button size="sm" onClick={() => bulk.mutate('enable')}>Enable</Button><Button size="sm" onClick={() => bulk.mutate('disable')}>Disable</Button><Button size="sm" onClick={() => bulk.mutate('health_check')}><RefreshCw size={14} />Health check</Button><Button size="sm" onClick={() => setMoveGroupOpen(true)}>Move group</Button><Button size="sm" variant="danger" onClick={() => setDeleteOpen(true)}><Trash2 size={13} />Delete</Button><button className="clear-selection" onClick={() => setSelected(new Set())}>Clear</button></div>}
        {result.isLoading && <div className="panel-pad"><Skeleton rows={10} /></div>}
        {result.isError && <div className="panel-pad"><ErrorState error={result.error} onRetry={() => result.refetch()} /></div>}
        {result.isSuccess && rows.length === 0 && <EmptyState title="No credentials configured" description="Add an authorized provider API credential to enable routing." action={<Button variant="primary" onClick={() => setAddOpen(true)}><Plus size={15} />Add your first credential</Button>} />}
        {rows.length > 0 && view === 'cards' && <div className="credential-grid">{rows.map((credential) => <CredentialCard key={String(credential.id)} credential={credential} checked={selected.has(String(credential.id))} onCheck={(checked) => select(String(credential.id), checked)} onDetail={() => setDetail(credential)} onTest={() => testCredential.mutate(String(credential.id))} />)}</div>}
        {rows.length > 0 && view === 'table' && <CredentialTable rows={rows} selected={selected} onSelect={select} onDetail={setDetail} />}
        <Pagination page={page} pageSize={24} total={result.data?.total || 0} onChange={setPage} />
      </section>
      <Modal open={addOpen} onClose={() => setAddOpen(false)} title="Add OpenAI credential" description="RelayDock validates this administrator-supplied key against the official API before activating it." wide footer={<><Button onClick={() => setAddOpen(false)}>Cancel</Button><Button disabled={!form.provider_id || !form.name || !form.secret || save.isPending} onClick={() => save.mutate('disabled')}>Save disabled</Button><SubmitButton disabled={!form.provider_id || !form.name || !form.secret} pending={save.isPending} form="credential-form">Validate & save</SubmitButton></>}>
        <Form id="credential-form" className="form-grid" onSubmit={() => save.mutateAsync('validate')}>
          <label><span>Name *</span><input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="Production · East 01" /></label>
          <label><span>Provider ID *</span><input required value={form.provider_id} onChange={(event) => setForm({ ...form, provider_id: event.target.value })} placeholder="OpenAI provider ID" /></label>
          <label><span>Credential group</span><input value={form.group_id} onChange={(event) => setForm({ ...form, group_id: event.target.value })} placeholder="Optional group ID" /></label>
          <label className="full-span"><span>OpenAI API key *</span><input required type="password" autoComplete="off" value={form.secret} onChange={(event) => setForm({ ...form, secret: event.target.value })} placeholder="Enter a newly issued official API key" /><small>The secret is encrypted at rest and cannot be retrieved after saving.</small></label>
          <label><span>Organization ID</span><input value={form.organization_id} onChange={(event) => setForm({ ...form, organization_id: event.target.value })} placeholder="Optional" /></label>
          <label><span>Project ID</span><input value={form.project_id} onChange={(event) => setForm({ ...form, project_id: event.target.value })} placeholder="Optional" /></label>
          <label className="full-span"><span>Scheduler tags</span><input value={form.tags} onChange={(event) => setForm({ ...form, tags: event.target.value })} placeholder="region:apac, tier:production" /><small>Comma-separated labels used by route required/excluded tag constraints.</small></label>
          <label><span>Weight</span><input type="number" min="0" value={form.weight} onChange={(event) => setForm({ ...form, weight: event.target.value })} /></label>
          <label><span>Priority</span><input type="number" min="0" value={form.priority} onChange={(event) => setForm({ ...form, priority: event.target.value })} /></label>
          <label><span>Max concurrency</span><input type="number" min="1" value={form.max_concurrency} onChange={(event) => setForm({ ...form, max_concurrency: event.target.value })} /></label>
          {save.isError && <div className="form-error full-span">{save.error instanceof Error ? save.error.message : 'Credential validation failed.'}</div>}
        </Form>
      </Modal>
      <Modal open={importOpen} onClose={() => setImportOpen(false)} title="Import authorized credentials" description="Paste 1 to 25 already-issued official provider API credentials. Account passwords, browser cookies, consumer sessions, and registration data are not accepted." wide footer={<><Button onClick={() => setImportOpen(false)}>Cancel</Button><SubmitButton form="credential-import-form" pending={importCredentials.isPending}>Validate & import</SubmitButton></>}>
        <Form id="credential-import-form" className="form-grid" onSubmit={() => importCredentials.mutateAsync()}>
          <label className="full-span"><span>Credential JSON *</span><textarea required rows={12} spellCheck={false} value={importText} onChange={(event) => setImportText(event.target.value)} placeholder={'[{\n  "name": "Production key 01",\n  "api_key": "<official provider key>",\n  "provider_id": "<provider id>",\n  "group_id": "<optional group id>",\n  "weight": 100\n}]'} /><small>Secrets are sent only to RelayDock, encrypted before storage, and never returned in list or export responses.</small></label>
          {importCredentials.isError && <div className="form-error full-span">{importCredentials.error instanceof Error ? importCredentials.error.message : 'Credential import failed.'}</div>}
        </Form>
      </Modal>
      <Modal open={moveGroupOpen} onClose={() => setMoveGroupOpen(false)} title="Move selected credentials" description="Add every selected credential to the target scheduling group." footer={<><Button onClick={() => setMoveGroupOpen(false)}>Cancel</Button><Button variant="primary" disabled={!moveGroupId || bulk.isPending} onClick={() => bulk.mutate(`move:${moveGroupId}`)}>{bulk.isPending ? 'Moving…' : 'Move credentials'}</Button></>}><div className="form-grid"><label className="full-span"><span>Target group ID *</span><input required value={moveGroupId} onChange={(event) => setMoveGroupId(event.target.value)} placeholder="Credential group ID" /></label></div></Modal>
      <Modal open={deleteOpen} onClose={() => setDeleteOpen(false)} title="Delete selected credentials" description="This permanently removes the selected encrypted credentials from RelayDock." footer={<><Button onClick={() => setDeleteOpen(false)}>Cancel</Button><Button variant="danger" disabled={bulk.isPending} onClick={() => bulk.mutate('delete')}>{bulk.isPending ? 'Deleting…' : `Delete ${selected.size} credentials`}</Button></>}><div className="inline-warning">Routes relying on these credentials may lose capacity. This action is recorded in the audit log.</div></Modal>
      <Drawer open={Boolean(detail)} onClose={() => setDetail(null)} title="Credential details">{detail && <><CredentialDetails credential={detail} /><CredentialTagEditor key={String(detail.id)} credential={detail} onSaved={(value) => setDetail(value)} /><div className="drawer-actions"><Button onClick={() => testCredential.mutate(String(detail.id))}>Test connection</Button><Button onClick={() => { const nextStatus = String(detail.status).toUpperCase() === 'ACTIVE' ? 'DISABLED' : 'ACTIVE'; void api(`/credentials/${detail.id}/status`, { method: 'PATCH', body: JSON.stringify({ status: nextStatus }) }).then(() => { toast(`Credential ${nextStatus.toLowerCase()}`); setDetail(null); void queryClient.invalidateQueries({ queryKey: ['credentials'] }) }).catch((error) => toast(error instanceof Error ? error.message : 'Status update failed', 'danger')) }}>{String(detail.status).toUpperCase() === 'ACTIVE' ? 'Disable' : 'Enable'}</Button><Button variant="danger" onClick={() => { setSelected(new Set([String(detail.id)])); setDetail(null); setDeleteOpen(true) }}><Trash2 size={13} />Delete</Button></div></>}</Drawer>
    </div>
  )
}

function CockpitPanel({ pool, loading, error, refreshing, testing, onRefresh, onTest, onRetry }: { pool?: CockpitPool; loading: boolean; error?: unknown; refreshing: boolean; testing: boolean; onRefresh: () => void; onTest: () => void; onRetry: () => void }) {
  return <section className="resource-panel cockpit-panel">
    <div className="cockpit-heading"><div><div className="eyebrow-row"><span className="live-dot" />COCKPIT AUTHORIZED ACCOUNTS</div><h2>Cockpit account quota</h2><p>Read-only quota and authorization status from the local Cockpit sidecar. No OAuth tokens, cookies, or passwords enter RelayDock.</p></div><div className="header-actions"><Button onClick={onRefresh} disabled={refreshing}><RefreshCw size={14} className={refreshing ? 'spin' : ''} />{refreshing ? 'Loading…' : 'Refresh snapshot'}</Button><Button variant="primary" onClick={onTest} disabled={testing || !pool?.test_configured} title={!pool?.test_configured ? 'Live test is not configured in the RelayDock container. The latest host-side test is shown below.' : undefined}><Activity size={14} />{testing ? 'Loading…' : 'Test sidecar'}</Button></div></div>
    {loading && <div className="panel-pad"><Skeleton rows={4} /></div>}
    {Boolean(error) && <div className="panel-pad"><ErrorState error={error} onRetry={onRetry} /></div>}
    {!loading && !error && !pool?.configured && <EmptyState title="Snapshot unavailable" description="Run scripts/sync-cockpit.ps1 on the host to create a sanitized account snapshot." />}
    {pool?.configured && <>
      <div className="cockpit-summary"><span><ShieldCheck size={15} />{pool.accounts.length} authorized accounts</span><span><Gauge size={15} />{pool.accounts.filter((account) => account.status === 'ready').length} ready</span><span><RefreshCw size={14} />Snapshot {formatDate(pool.generated_at)}</span>{pool.last_test?.ok && <span className="cockpit-verified"><CheckCircle2 size={15} />Live verification passed · {pool.last_test.model} · {pool.last_test.latency_ms} ms</span>}</div>
      <div className="cockpit-account-grid">{pool.accounts.map((account) => {
        const remaining = Math.max(0, Math.min(100, Number(account.remaining_percent || 0)))
        return <article className="cockpit-account" key={account.id}><div className="cockpit-account-head"><div><strong>{account.email_masked}</strong><small>{account.auth_kind.toUpperCase()} · {account.id}</small></div><div className="badge-row"><Badge tone="violet">{account.plan.toUpperCase()}</Badge><Badge tone={account.status === 'ready' ? 'success' : 'warning'} dot>{account.status === 'ready' ? 'Ready' : 'Quota exhausted'}</Badge></div></div><div className="cockpit-quota"><div><span>Primary window</span><strong>{remaining}%</strong></div><div className="meter-track"><i className={remaining <= 10 ? 'low' : ''} style={{ width: `${remaining}%` }} /></div></div><div className="cockpit-meta"><span>Remaining quota <b>{formatNumber(account.remaining_quota, false)}</b></span><span>Secondary window <b>{account.secondary_percent}%</b></span><span>Resets <b>{formatDate(account.reset_at)}</b></span><span>Subscription expires <b>{formatDate(account.subscription_expires_at)}</b></span><span>Last updated <b>{formatDate(account.updated_at)}</b></span></div></article>
      })}</div>
      <div className="cockpit-security"><ShieldCheck size={16} /><div><strong>Security boundary</strong><p>Only masked email, plan, status, quota percentage, and timestamps are stored in this snapshot.</p></div></div>
    </>}
  </section>
}

function CredentialCard({ credential, checked, onCheck, onDetail, onTest }: { credential: Credential; checked: boolean; onCheck: (checked: boolean) => void; onDetail: () => void; onTest: () => void }) {
  const health = String(credential.current_health || credential.health || 'unknown')
  return <article className={`credential-card ${checked ? 'selected' : ''}`}><div className="credential-card-head"><input type="checkbox" checked={checked} onChange={(event) => onCheck(event.target.checked)} aria-label={`Select ${credential.name}`} /><div className="credential-identity"><span className="provider-glyph">AI</span><div><strong>{credential.name || 'Unnamed credential'}</strong><small>{credential.provider_name || providerName(credential.provider)} · {credential.project_id || 'No project ID'}</small></div></div><button className="icon-button small" onClick={onDetail}><MoreHorizontal size={16} /></button></div><div className="credential-badges"><StatusBadge value={credential.status} /><StatusBadge value={health} />{credential.group_name && <Badge tone="violet">{credential.group_name}</Badge>}</div><div className="credential-secret"><span>Secret</span><code>••••••••{credential.secret_last4 || '••••'}</code></div><div className="load-meter"><div><span>Concurrency</span><strong>{formatNumber(credential.active_requests, false)} / {formatNumber(credential.max_concurrency, false)}</strong></div><div className="meter-track"><i style={{ width: `${Math.min(100, Number(credential.active_requests || 0) / Math.max(1, Number(credential.max_concurrency || 1)) * 100)}%` }} /></div></div><div className="credential-kpis"><div><span>Recent RPM</span><strong>{formatNumber(credential.recent_rpm)}</strong></div><div><span>Recent TPM</span><strong>{formatNumber(credential.recent_tpm)}</strong></div><div><span>Error rate</span><strong>{credential.error_rate === undefined ? '—' : `${Number(credential.error_rate).toFixed(2)}%`}</strong></div></div>{credential.cooldown_until && <div className="cooldown-note"><TimerReset size={14} />Cooldown until {formatDate(credential.cooldown_until)}</div>}<div className="credential-meta"><span>Weight <b>{formatNumber(credential.weight, false)}</b></span><span>Priority <b>{formatNumber(credential.priority, false)}</b></span><span>Last request <b>{formatDate(credential.last_request_at)}</b></span></div><div className="credential-card-foot"><div className="badge-row">{credential.tags?.slice(0, 2).map((tag) => <Badge key={tag}>{tag}</Badge>)}</div><div><Button size="sm" onClick={onTest}>Test</Button><Button size="sm" variant="ghost" onClick={onDetail}>Details</Button></div></div></article>
}

function CredentialTable({ rows, selected, onSelect, onDetail }: { rows: Credential[]; selected: Set<string>; onSelect: (id: string, checked: boolean) => void; onDetail: (row: Credential) => void }) {
  return <div className="table-wrap"><table className="data-table credential-table"><thead><tr><th /><th>Credential</th><th>Status</th><th>Group</th><th>Load</th><th>RPM / TPM</th><th>Error</th><th>Last request</th><th /></tr></thead><tbody>{rows.map((row) => <tr key={String(row.id)}><td className="check-cell"><input type="checkbox" checked={selected.has(String(row.id))} onChange={(event) => onSelect(String(row.id), event.target.checked)} /></td><td><div className="primary-cell"><strong>{row.name}</strong><code>••••{row.secret_last4 || '••••'}</code></div></td><td><StatusBadge value={row.status} /></td><td>{row.group_name || '—'}</td><td>{formatNumber(row.active_requests, false)} / {formatNumber(row.max_concurrency, false)}</td><td>{formatNumber(row.recent_rpm)} / {formatNumber(row.recent_tpm)}</td><td>{row.error_rate === undefined ? '—' : `${Number(row.error_rate).toFixed(2)}%`}</td><td>{formatDate(row.last_request_at)}</td><td><Button variant="ghost" size="sm" onClick={() => onDetail(row)}><MoreHorizontal size={15} /></Button></td></tr>)}</tbody></table></div>
}

function CredentialDetails({ credential }: { credential: Credential }) {
  const rows = [['Provider', credential.provider_name || providerName(credential.provider)], ['Project', credential.project_id], ['Group', credential.group_name], ['Status', credential.status], ['Health', credential.current_health || credential.health], ['Weight', credential.weight], ['Priority', credential.priority], ['Max concurrency', credential.max_concurrency], ['Last success', formatDate(credential.last_success_at)], ['Last failure', formatDate(credential.last_failure_at)], ['Cooldown until', formatDate(credential.cooldown_until)]]
  return <><div className="detail-hero"><span className="provider-glyph"><CheckCircle2 size={18} /></span><div><strong>{credential.name}</strong><code>••••••••{credential.secret_last4 || '••••'}</code></div></div><div className="detail-list">{rows.map(([label, value]) => <div key={String(label)}><span>{String(label)}</span><strong>{value === undefined || value === '' ? '—' : String(value)}</strong></div>)}</div><div className="inline-note">The original provider secret is never returned by the API. Enter a new value to replace it.</div></>
}

function CredentialTagEditor({ credential, onSaved }: { credential: Credential; onSaved: (credential: Credential) => void }) {
  const [value, setValue] = useState((credential.tags || []).join(', '))
  const [dirty, setDirty] = useState(false)
  const client = useQueryClient()
  const toast = useToast()
  const queryKey = ['credential-tags', credential.id]
  const tags = useQuery({
    queryKey,
    queryFn: () => api<{ tags: string[] }>(`/credentials/${credential.id}/tags`),
    enabled: Boolean(credential.id),
  })
  useEffect(() => {
    if (tags.data && !dirty) setValue(tags.data.tags.join(', '))
  }, [dirty, tags.data])
  const save = useMutation({
    mutationFn: () => api<{ tags: string[] }>(`/credentials/${credential.id}/tags`, { method: 'PUT', body: JSON.stringify({ tags: splitTags(value) }) }),
    onSuccess: (updated) => { setDirty(false); client.setQueryData(queryKey, updated); onSaved({ ...credential, tags: updated.tags }); void client.invalidateQueries({ queryKey: ['credentials'] }); toast('Credential scheduler tags saved') },
  })
  return <div className="credential-tag-editor"><div className="section-heading"><div><strong>Scheduler tags</strong><small>Required tags must all match; any excluded tag removes this credential from the candidate set.</small></div></div><div><input value={value} onChange={(event) => { setValue(event.target.value); setDirty(true) }} placeholder="region:apac, tier:production" /><Button size="sm" onClick={() => save.mutate()} disabled={save.isPending || tags.isLoading}>{save.isPending ? 'Saving…' : tags.isLoading ? 'Loading…' : 'Save tags'}</Button></div>{tags.isError && <div className="form-error">{tags.error instanceof Error ? tags.error.message : 'Unable to load tags.'}</div>}{save.isError && <div className="form-error">{save.error instanceof Error ? save.error.message : 'Unable to save tags.'}</div>}</div>
}

function splitTags(value: string) {
  return [...new Set(value.split(',').map((tag) => tag.trim().toLowerCase()).filter(Boolean))]
}

function providerName(provider: Credential['provider']) {
  return typeof provider === 'object' ? provider?.name || 'Provider' : String(provider || 'Provider')
}
