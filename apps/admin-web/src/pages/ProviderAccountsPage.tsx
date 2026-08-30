import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Building2, CircleDollarSign, Plus, RefreshCw, RotateCcw, ShieldCheck, UserRoundCog } from 'lucide-react'
import { api, asPage, formatDate, formatMoneyString } from '../lib/api'
import { Badge, Button, DataTable, EmptyState, ErrorState, Form, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type Capability = {
  provider_id: string; provider_name: string; provider_slug: string; provider_type: string; mode: string; enabled: boolean
  supports_automatic_binding: boolean; supports_automatic_credit: boolean; supports_refresh: boolean
  free_test_model?: string; reason?: string; documentation_url?: string
}
type Binding = {
  id: string; organization_id: string; user_id: string; user_email?: string; provider_id: string; provider_name: string
  provider_type: string; provisioning_mode: string; status: string; external_account_id?: string; external_project_id?: string
  credential_id?: string; allocated_amount: string; currency?: string; last_synced_at?: string; created_at: string
}
type Job = {
  id: string; binding_id: string; provider_name: string; operation: string; status: string; amount?: string; currency?: string
  attempts: number; max_attempts: number; external_reference?: string; error_code?: string; error_detail?: string
  created_at: string; completed_at?: string
}
type Option = { id: string; name?: string; email?: string; display_name?: string }

const emptyForm = { organization_id: '', user_id: '', provider_id: '', provisioning_mode: '', automatic: true, external_account_id: '', external_project_id: '', credential_id: '' }

export function ProviderAccountsPage() {
  const client = useQueryClient()
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState(emptyForm)
  const capabilities = useQuery({ queryKey: ['provider-provisioning-capabilities'], queryFn: () => api<unknown>('/provider-provisioning/capabilities').then(asPage<Capability>) })
  const bindings = useQuery({ queryKey: ['provider-accounts'], queryFn: () => api<unknown>('/provider-accounts', { query: { limit: 100 } }).then(asPage<Binding>), refetchInterval: 15_000 })
  const jobs = useQuery({ queryKey: ['provider-provisioning-jobs'], queryFn: () => api<unknown>('/provider-provisioning/jobs', { query: { limit: 100 } }).then(asPage<Job>), refetchInterval: 10_000 })
  const organizations = useQuery({ queryKey: ['provider-account-organizations'], queryFn: () => api<unknown>('/organizations', { query: { limit: 100 } }).then(asPage<Option>) })
  const users = useQuery({ queryKey: ['provider-account-users'], queryFn: () => api<unknown>('/users', { query: { limit: 100 } }).then(asPage<Option>) })
  const capabilityRows = useMemo(() => capabilities.data?.items || [], [capabilities.data?.items])
  const selectedCapability = useMemo(() => capabilityRows.find((item) => item.provider_id === form.provider_id), [capabilityRows, form.provider_id])

  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['provider-provisioning-capabilities'] })
    void client.invalidateQueries({ queryKey: ['provider-accounts'] })
    void client.invalidateQueries({ queryKey: ['provider-provisioning-jobs'] })
  }
  const create = useMutation({
    mutationFn: () => api('/provider-accounts', {
      method: 'POST', headers: { 'Idempotency-Key': `provider-binding-${crypto.randomUUID()}` }, body: JSON.stringify({
        ...form, provisioning_mode: form.provisioning_mode || selectedCapability?.mode,
        credential_id: form.credential_id || undefined,
      }),
    }),
    onSuccess: () => { setOpen(false); setForm(emptyForm); toast('Provider account binding queued'); refresh() },
  })
  const retry = useMutation({
    mutationFn: (jobID: string) => api(`/provider-provisioning/jobs/${jobID}/retry`, { method: 'POST' }),
    onSuccess: () => { toast('Provisioning job queued for retry'); refresh() },
  })

  if (capabilities.isLoading || bindings.isLoading || jobs.isLoading) return <div className="page-stack"><Panel><Skeleton rows={9} /></Panel></div>
  if (capabilities.isError || bindings.isError || jobs.isError) return <ErrorState error={capabilities.error || bindings.error || jobs.error} onRetry={refresh} />

  const bindingRows = bindings.data?.items || []
  const jobRows = jobs.data?.items || []
  const automaticBindingCount = capabilityRows.filter((item) => item.enabled && item.supports_automatic_binding).length
  const automaticCreditCount = capabilityRows.filter((item) => item.enabled && item.supports_automatic_credit).length

  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><UserRoundCog size={14} />MULTI-PROVIDER ACCOUNT PROVISIONING</div><h1>Provider accounts</h1><p>Bind RelayDock users to official enterprise projects, reviewed manual accounts, or BYOK credentials. Payment credit and upstream allocation remain separate, auditable states.</p></div><div className="header-actions"><Button onClick={refresh}><RefreshCw size={14} />Refresh</Button><Button variant="primary" onClick={() => setOpen(true)}><Plus size={14} />Create binding</Button></div></div>
    <div className="security-card tenant-safety"><ShieldCheck size={20} /><div><strong>Official capabilities only</strong><p>Automatic registration is enabled only when a provider documents a project/sub-account API. Consumer signup, CAPTCHA bypass, trial farming, and fabricated credit transfer are not supported.</p></div><Badge tone="success">Fail closed</Badge></div>
    <div className="metric-grid compact-metrics">
      <Panel title="Active bindings"><strong className="billing-total">{bindingRows.filter((item) => item.status === 'ACTIVE').length}</strong></Panel>
      <Panel title="Automatic binding"><strong className="billing-total">{automaticBindingCount}</strong></Panel>
      <Panel title="Automatic upstream credit"><strong className="billing-total">{automaticCreditCount}</strong></Panel>
      <Panel title="Jobs needing attention"><strong className="billing-total">{jobRows.filter((item) => ['FAILED', 'ACTION_REQUIRED'].includes(item.status)).length}</strong></Panel>
    </div>

    <Panel title="Platform capabilities" description="A provider may support enterprise project creation without exposing a project-wallet top-up API.">
      {capabilityRows.length === 0 ? <EmptyState title="No Providers" /> : <DataTable rows={capabilityRows} rowKey={(row) => row.provider_id} columns={[
        { key: 'provider', label: 'Provider', render: (row) => <div className="primary-cell"><strong>{row.provider_name}</strong><code>{row.provider_type}</code></div> },
        { key: 'mode', label: 'Mode', render: (row) => <Badge tone="violet">{row.mode}</Badge> },
        { key: 'binding', label: 'Auto binding', render: (row) => <StatusBadge value={row.enabled && row.supports_automatic_binding ? 'enabled' : 'disabled'} /> },
        { key: 'credit', label: 'Upstream credit', render: (row) => <StatusBadge value={row.enabled && row.supports_automatic_credit ? 'enabled' : 'unsupported'} /> },
        { key: 'reason', label: 'Boundary', render: (row) => <span className="muted-cell">{row.reason || '—'}</span> },
        { key: 'docs', label: '', render: (row) => row.documentation_url ? <a className="table-link" href={row.documentation_url} target="_blank" rel="noreferrer">Official docs</a> : '—' },
      ]} />}
    </Panel>

    <Panel title="User bindings" description="External identifiers are non-secret. Provisioned API keys are encrypted in the existing credential pool.">
      {bindingRows.length === 0 ? <EmptyState title="No provider account bindings" description="Create an official, BYOK, or reviewed manual binding." /> : <DataTable rows={bindingRows} rowKey={(row) => row.id} columns={[
        { key: 'user', label: 'RelayDock user', render: (row) => <div className="primary-cell"><strong>{row.user_email || row.user_id}</strong><code>{row.organization_id}</code></div> },
        { key: 'provider', label: 'Provider', render: (row) => <div className="primary-cell"><strong>{row.provider_name}</strong><code>{row.provisioning_mode}</code></div> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'external', label: 'External project / account', render: (row) => <div className="primary-cell"><strong>{row.external_project_id || '—'}</strong><code>{row.external_account_id || '—'}</code></div> },
        { key: 'credential', label: 'Pool credential', render: (row) => row.credential_id ? <Badge tone="success">Encrypted</Badge> : <span className="muted-cell">None</span> },
        { key: 'credit', label: 'Allocated upstream', render: (row) => formatMoneyString(row.allocated_amount, row.currency || 'USD') },
        { key: 'sync', label: 'Last sync', render: (row) => formatDate(row.last_synced_at || row.created_at) },
      ]} />}
    </Panel>

    <Panel title="Provisioning jobs" description="Verified payment replay cannot create a second allocation job; each recharge order is unique here.">
      {jobRows.length === 0 ? <EmptyState title="No provisioning jobs" /> : <DataTable rows={jobRows} rowKey={(row) => row.id} columns={[
        { key: 'provider', label: 'Provider / operation', render: (row) => <div className="primary-cell"><strong>{row.provider_name}</strong><code>{row.operation}</code></div> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'amount', label: 'Amount', render: (row) => row.amount ? formatMoneyString(row.amount, row.currency) : '—' },
        { key: 'attempts', label: 'Attempts', render: (row) => `${row.attempts} / ${row.max_attempts}` },
        { key: 'reference', label: 'External reference', render: (row) => row.external_reference || row.error_code || '—' },
        { key: 'created', label: 'Created', render: (row) => formatDate(row.created_at) },
        { key: 'action', label: '', render: (row) => ['FAILED', 'ACTION_REQUIRED'].includes(row.status) ? <Button size="sm" variant="ghost" disabled={retry.isPending} onClick={() => retry.mutate(row.id)}><RotateCcw size={13} />Retry</Button> : null },
      ]} />}
    </Panel>

    <Modal open={open} onClose={() => setOpen(false)} title="Create provider account binding" description="Automatic mode creates only documented enterprise projects/service accounts. Manual and BYOK modes require an existing upstream reference or credential." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="create-provider-binding" pending={create.isPending}>Create binding</SubmitButton></>}>
      <Form id="create-provider-binding" className="form-grid" onSubmit={() => create.mutateAsync()}>
        <label><span>Organization *</span><select required value={form.organization_id} onChange={(event) => setForm({ ...form, organization_id: event.target.value })}><option value="">Select organization</option>{(organizations.data?.items || []).map((item) => <option key={item.id} value={item.id}>{item.name || item.id}</option>)}</select></label>
        <label><span>RelayDock user *</span><select required value={form.user_id} onChange={(event) => setForm({ ...form, user_id: event.target.value })}><option value="">Select user</option>{(users.data?.items || []).map((item) => <option key={item.id} value={item.id}>{item.email || item.display_name || item.id}</option>)}</select></label>
        <label className="full-span"><span>Provider *</span><select required value={form.provider_id} onChange={(event) => { const selected = capabilityRows.find((item) => item.provider_id === event.target.value); setForm({ ...form, provider_id: event.target.value, provisioning_mode: selected?.mode || '', automatic: Boolean(selected?.enabled && selected.supports_automatic_binding) }) }}><option value="">Select provider</option>{capabilityRows.map((item) => <option key={item.provider_id} value={item.provider_id}>{item.provider_name} · {item.mode}</option>)}</select></label>
        <label><span>Mode</span><select value={form.provisioning_mode} onChange={(event) => setForm({ ...form, provisioning_mode: event.target.value, automatic: ['OFFICIAL_ENTERPRISE', 'MOCK_ENTERPRISE'].includes(event.target.value) })}><option value={selectedCapability?.mode || ''}>{selectedCapability?.mode || 'Select provider'}</option><option value="BYOK">BYOK</option><option value="MANUAL">MANUAL</option></select></label>
        <label><span>Automation</span><select value={form.automatic ? 'automatic' : 'manual'} onChange={(event) => setForm({ ...form, automatic: event.target.value === 'automatic' })}><option value="automatic" disabled={!selectedCapability?.enabled || !selectedCapability.supports_automatic_binding}>Automatic</option><option value="manual">Reviewed manual</option></select></label>
        {!form.automatic && <><label><span>External project ID</span><input value={form.external_project_id} onChange={(event) => setForm({ ...form, external_project_id: event.target.value })} /></label><label><span>External account ID</span><input value={form.external_account_id} onChange={(event) => setForm({ ...form, external_account_id: event.target.value })} /></label><label className="full-span"><span>Existing credential ID (BYOK)</span><input value={form.credential_id} onChange={(event) => setForm({ ...form, credential_id: event.target.value })} /></label></>}
        {selectedCapability && <div className="inline-note full-span"><Building2 size={15} /><div><strong>{selectedCapability.supports_automatic_credit ? 'Payment-linked upstream allocation supported' : 'No payment-linked upstream allocation'}</strong><p>{selectedCapability.reason}</p>{selectedCapability.free_test_model && <small><CircleDollarSign size={12} /> Free test model: {selectedCapability.free_test_model}</small>}</div></div>}
        {create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Unable to create binding.'}</div>}
      </Form>
    </Modal>
  </div>
}
