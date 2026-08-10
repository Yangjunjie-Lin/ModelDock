import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Copy, KeyRound, Plus, RefreshCw, ShieldCheck, Trash2 } from 'lucide-react'
import { api, asPage, formatDate, formatNumber } from '../lib/api'
import { consoleV2Paths, useProjectScope } from '../lib/project-scope'
import { Badge, Button, DataTable, type Column, EmptyState, ErrorState, Form, Modal, Pagination, SearchInput, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type ApiKeyRow = Record<string, unknown> & {
  id?: string
  project_id?: string
  name?: string
  key_prefix?: string
  status?: string
  created_at?: string
  last_used_at?: string
  expires_at?: string
  rate_limit_rpm?: number
  rate_limit_tpm?: number
  allowed_models?: string[]
}

type SecretResult = { value: string; title: string; description: string }

export function ApiKeysPage() {
  const scope = useProjectScope()
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [secret, setSecret] = useState<SecretResult | null>(null)
  const [rotateKey, setRotateKey] = useState<ApiKeyRow | null>(null)
  const [finalizeKey, setFinalizeKey] = useState<ApiKeyRow | null>(null)
  const [rotation, setRotation] = useState({ grace_minutes: '60' })
  const [form, setForm] = useState({ name: '', environment: 'live', rate_limit_rpm: '', rate_limit_tpm: '', expires_at: '', allowed_models: [] as string[] })
  const toast = useToast()
  const client = useQueryClient()
  const projectID = scope.projectID
  const result = useQuery({
    queryKey: ['console-api-keys', projectID, search, page],
    queryFn: () => api<unknown>(consoleV2Paths.projectAPIKeys(projectID), { query: { project_id: projectID, search, limit: 20, offset: (page - 1) * 20 } }).then(asPage<ApiKeyRow>),
    enabled: Boolean(projectID),
  })
  const models = useQuery({
    queryKey: ['console-key-models', projectID],
    queryFn: () => api<unknown>(consoleV2Paths.projectModels(projectID), { query: { project_id: projectID } }).then(asPage<Record<string, unknown>>),
    enabled: Boolean(projectID),
  })
  const create = useMutation({
    mutationFn: () => api<Record<string, unknown>>(consoleV2Paths.projectAPIKeys(projectID), { method: 'POST', body: JSON.stringify({ ...form, organization_id: scope.organizationID, project_id: projectID, rate_limit_rpm: form.rate_limit_rpm ? Number(form.rate_limit_rpm) : undefined, rate_limit_tpm: form.rate_limit_tpm ? Number(form.rate_limit_tpm) : undefined, expires_at: form.expires_at ? new Date(`${form.expires_at}T23:59:59Z`).toISOString() : undefined }) }),
    onSuccess: (response) => {
      const value = String(response.secret || response.key || '')
      setCreateOpen(false)
      setForm({ name: '', environment: 'live', rate_limit_rpm: '', rate_limit_tpm: '', expires_at: '', allowed_models: [] })
      void client.invalidateQueries({ queryKey: ['console-api-keys', projectID] })
      if (value) setSecret({ value, title: 'Save your API key', description: 'This project key is shown exactly once.' })
      else toast('API key created, but the server did not return a displayable secret', 'danger')
    },
  })
  const revoke = useMutation({
    mutationFn: (id: string) => api(consoleV2Paths.projectAPIKey(projectID, id), { method: 'DELETE' }),
    onSuccess: () => { toast('API key revoked'); void client.invalidateQueries({ queryKey: ['console-api-keys', projectID] }) },
  })
  const rotate = useMutation({
    mutationFn: () => api<Record<string, unknown>>(consoleV2Paths.apiKeyRotate(projectID, String(rotateKey?.id)), { method: 'POST', body: JSON.stringify({ grace_seconds: Number(rotation.grace_minutes) * 60 }) }),
    onSuccess: (response) => {
      const value = String(response.secret || response.key || '')
      setRotateKey(null)
      setRotation({ grace_minutes: '60' })
      void client.invalidateQueries({ queryKey: ['console-api-keys', projectID] })
      if (value) setSecret({ value, title: 'Save the rotated API key', description: 'The old key remains valid only for the configured grace period.' })
      else toast('Key rotated, but the new secret was not returned', 'danger')
    },
  })
  const finalize = useMutation({
    mutationFn: () => api(consoleV2Paths.apiKeyFinalize(projectID, String(finalizeKey?.id)), { method: 'POST', body: JSON.stringify({ version: 0 }) }),
    onSuccess: () => { setFinalizeKey(null); void client.invalidateQueries({ queryKey: ['console-api-keys', projectID] }); toast('Rotation finalized; all grace versions are revoked') },
  })

  const columns: Column<ApiKeyRow>[] = [
    { key: 'name', label: 'Name', render: (row) => <div className="primary-cell"><strong>{row.name || 'Unnamed key'}</strong><code>{row.key_prefix || 'rdk_••••'}</code></div> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'models', label: 'Allowed models', render: (row) => row.allowed_models?.length ? <div className="badge-row">{row.allowed_models.slice(0, 2).map((model) => <Badge key={model}>{model}</Badge>)}</div> : <span className="muted-cell">Project grants</span> },
    { key: 'rate', label: 'Rate limit', render: (row) => <div className="primary-cell"><strong>{row.rate_limit_rpm ? `${formatNumber(row.rate_limit_rpm, false)} RPM` : 'Default'}</strong><small>{row.rate_limit_tpm ? `${formatNumber(row.rate_limit_tpm)} TPM` : 'Default TPM'}</small></div> },
    { key: 'created', label: 'Created', render: (row) => <span className="muted-cell">{formatDate(row.created_at)}</span> },
    { key: 'last_used', label: 'Last used', render: (row) => <span className="muted-cell">{formatDate(row.last_used_at)}</span> },
    { key: 'actions', label: '', className: 'action-cell', render: (row) => <div className="row-actions"><Button size="sm" onClick={() => setRotateKey(row)} disabled={row.status !== 'ACTIVE'}><RefreshCw size={13} />Rotate</Button><Button variant="ghost" size="sm" onClick={() => setFinalizeKey(row)} disabled={row.status !== 'ACTIVE'} title="Revoke all prior grace versions"><CheckCircle2 size={14} /></Button><Button variant="ghost" size="sm" onClick={() => row.id && revoke.mutate(row.id)} aria-label="Revoke key"><Trash2 size={14} /></Button></div> },
  ]
  const rows = (result.data?.items || []).filter((row) => row.project_id === projectID)

  return <div className="page-stack">
    <div className="page-header"><div><h1>API keys</h1><p>Create and rotate project-scoped RelayDock keys. Full secrets are shown exactly once.</p></div><Button variant="primary" onClick={() => setCreateOpen(true)} disabled={!projectID}><Plus size={15} />Create API key</Button></div>
    <div className="key-security-note"><ShieldCheck size={17} /><div><strong>Versioned keys are hashed at rest</strong><p>Rotation preserves policy and accounting identity. Finalization immediately revokes all older grace versions.</p></div></div>
    {!projectID && !scope.loading && <EmptyState title="Select a project" description="Keys are issued and listed only within the selected project." />}
    {projectID && <section className="resource-panel"><div className="resource-toolbar"><SearchInput value={search} onChange={setSearch} placeholder="Search API keys…" /></div>{result.isLoading && <div className="panel-pad"><Skeleton rows={7} /></div>}{result.isError && <div className="panel-pad"><ErrorState error={result.error} onRetry={() => result.refetch()} /></div>}{result.isSuccess && rows.length === 0 && <EmptyState title="No API keys" description="Create a project key to authenticate your first gateway request." action={<Button variant="primary" onClick={() => setCreateOpen(true)}><KeyRound size={15} />Create API key</Button>} />}{rows.length > 0 && <DataTable columns={columns} rows={rows} rowKey={(row) => String(row.id)} />}<Pagination page={page} total={result.data?.total || 0} onChange={setPage} /></section>}

    <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="Create project API key" description={`Issue a key owned by you in ${scope.project?.name || 'the selected project'}.`} footer={<><Button onClick={() => setCreateOpen(false)}>Cancel</Button><SubmitButton form="create-key-form" pending={create.isPending}>Create key</SubmitButton></>} wide>
      <Form id="create-key-form" className="form-grid" onSubmit={() => create.mutateAsync()}><label className="full-span"><span>Key name *</span><input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="Production application" /></label><label><span>Environment</span><select value={form.environment} onChange={(event) => setForm({ ...form, environment: event.target.value })}><option value="live">Live</option><option value="test">Test</option></select></label><label><span>Expires</span><input type="date" value={form.expires_at} onChange={(event) => setForm({ ...form, expires_at: event.target.value })} /></label><label><span>Requests per minute</span><input type="number" min="1" value={form.rate_limit_rpm} onChange={(event) => setForm({ ...form, rate_limit_rpm: event.target.value })} placeholder="60" /></label><label><span>Tokens per minute</span><input type="number" min="1" value={form.rate_limit_tpm} onChange={(event) => setForm({ ...form, rate_limit_tpm: event.target.value })} placeholder="100000" /></label><fieldset className="event-options full-span"><legend>Allowed models (empty uses all project grants)</legend>{models.data?.items.map((model) => { const alias = String(model.alias || model.id); return <label key={alias}><input type="checkbox" checked={form.allowed_models.includes(alias)} onChange={(event) => setForm({ ...form, allowed_models: event.target.checked ? [...form.allowed_models, alias] : form.allowed_models.filter((value) => value !== alias) })} /><span>{alias}</span></label> })}</fieldset>{create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Unable to create API key.'}</div>}</Form>
    </Modal>

    <Modal open={Boolean(rotateKey)} onClose={() => setRotateKey(null)} title="Rotate API key" description={`Create a new secret for ${rotateKey?.name || 'this key'} while keeping the old secret valid briefly.`} footer={<><Button onClick={() => setRotateKey(null)}>Cancel</Button><SubmitButton form="rotate-key-form" pending={rotate.isPending}>Rotate key</SubmitButton></>}><Form id="rotate-key-form" className="form-grid" onSubmit={() => rotate.mutateAsync()}><label className="full-span"><span>Grace period (minutes) *</span><input required type="number" min="1" max="1440" value={rotation.grace_minutes} onChange={(event) => setRotation({ grace_minutes: event.target.value })} /><small>RelayDock accepts 30 seconds to 24 hours; Console uses whole minutes.</small></label><div className="inline-warning full-span">Both old and new secrets authenticate during the grace period. Finalize once every workload uses the new secret.</div>{rotate.isError && <div className="form-error full-span">{rotate.error instanceof Error ? rotate.error.message : 'Unable to rotate key.'}</div>}</Form></Modal>

    <Modal open={Boolean(finalizeKey)} onClose={() => setFinalizeKey(null)} title="Finalize key rotation" description="Revoke every older grace version immediately. The newest active secret remains valid." footer={<><Button onClick={() => setFinalizeKey(null)}>Cancel</Button><Button variant="danger" disabled={finalize.isPending} onClick={() => finalize.mutate()}>{finalize.isPending ? 'Finalizing…' : 'Finalize rotation'}</Button></>}><div className="inline-warning">Confirm that all applications have switched to the newest secret. This action cannot restore an old secret.</div>{finalize.isError && <div className="form-error">{finalize.error instanceof Error ? finalize.error.message : 'Unable to finalize rotation.'}</div>}</Modal>

    <Modal open={Boolean(secret)} onClose={() => setSecret(null)} title={secret?.title || 'Save your API key'} description={secret?.description}><div className="one-time-label"><span><KeyRound size={14} />ONE-TIME SECRET</span><strong>Store this key in a secure secret manager.</strong></div><div className="secret-box"><code>{secret?.value}</code><Button onClick={() => { if (secret) void navigator.clipboard.writeText(secret.value); toast('API key copied') }}><Copy size={14} />Copy</Button></div><div className="inline-warning">Do not expose this key in browser code, source control, logs, or chat messages.</div><Button className="secret-done" variant="primary" onClick={() => setSecret(null)}>I have saved this key</Button></Modal>
  </div>
}
