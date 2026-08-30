import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Plus, Settings2, ShieldCheck, Trash2 } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import { useProjectScope } from '../lib/project-scope'
import { Button, DataTable, EmptyState, ErrorState, Form, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type BYOKCredential = {
  id: string
  provider_id: string
  provider_name?: string
  name: string
  secret_last4: string
  status: string
  ownership_confirmed_at?: string
  ownership_terms_version?: string
  byok_priority_section?: 'PRIORITIZED' | 'FALLBACK'
  shared_capacity_fallback?: 'ALWAYS' | 'OUTSIDE_FILTERS' | 'NEVER'
  model_filters?: string[]
  api_key_filters?: string[]
  member_filters?: string[]
}

type ProvisioningCapability = { provider_id: string; provider_name: string; provider_slug: string; enabled: boolean }
type PolicyDraft = {
  byok_priority_section: 'PRIORITIZED' | 'FALLBACK'
  shared_capacity_fallback: 'ALWAYS' | 'OUTSIDE_FILTERS' | 'NEVER'
  model_filters: string
  api_key_filters: string
  member_filters: string
}

const defaultPolicy: PolicyDraft = {
  byok_priority_section: 'PRIORITIZED',
  shared_capacity_fallback: 'ALWAYS',
  model_filters: '',
  api_key_filters: '',
  member_filters: '',
}

function splitList(value: string) {
  return [...new Set(value.split(',').map((item) => item.trim().toLowerCase()).filter(Boolean))]
}

function policyFromCredential(credential: BYOKCredential): PolicyDraft {
  return {
    byok_priority_section: credential.byok_priority_section || 'PRIORITIZED',
    shared_capacity_fallback: credential.shared_capacity_fallback || 'ALWAYS',
    model_filters: (credential.model_filters || []).join(', '),
    api_key_filters: (credential.api_key_filters || []).join(', '),
    member_filters: (credential.member_filters || []).join(', '),
  }
}

export function ByokPage() {
  const scope = useProjectScope()
  const client = useQueryClient()
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [policyCredential, setPolicyCredential] = useState<BYOKCredential | null>(null)
  const [providerID, setProviderID] = useState('')
  const [name, setName] = useState('')
  const [secret, setSecret] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const [policy, setPolicy] = useState<PolicyDraft>(defaultPolicy)

  const result = useQuery({
    queryKey: ['byok', scope.organizationID],
    enabled: Boolean(scope.organizationID),
    queryFn: () => api<unknown>('/byok/credentials', { query: { organization_id: scope.organizationID } }).then(asPage<BYOKCredential>),
  })
  const providers = useQuery({
    queryKey: ['provider-provisioning-capabilities'],
    queryFn: () => api<unknown>('/provider-provisioning/capabilities').then(asPage<ProvisioningCapability>),
  })

  const create = useMutation({
    mutationFn: () => api('/byok/credentials', {
      method: 'POST',
      body: JSON.stringify({
        provider_id: providerID,
        project_id: scope.projectID,
        name,
        secret,
        terms_version: 'byok-ownership-v1',
        ownership_confirmed: confirmed,
        byok_priority_section: policy.byok_priority_section,
        shared_capacity_fallback: policy.shared_capacity_fallback,
        model_filters: splitList(policy.model_filters),
        api_key_filters: splitList(policy.api_key_filters),
        member_filters: splitList(policy.member_filters),
      }),
    }),
    onSuccess: () => {
      setOpen(false)
      setSecret('')
      setConfirmed(false)
      setPolicy(defaultPolicy)
      void client.invalidateQueries({ queryKey: ['byok'] })
      toast('BYOK credential encrypted and saved')
    },
  })
  const updatePolicy = useMutation({
    mutationFn: () => api(`/byok/credentials/${policyCredential?.id}/routing-policy`, {
      method: 'PUT',
      body: JSON.stringify({
        organization_id: scope.organizationID,
        byok_priority_section: policy.byok_priority_section,
        shared_capacity_fallback: policy.shared_capacity_fallback,
        model_filters: splitList(policy.model_filters),
        api_key_filters: splitList(policy.api_key_filters),
        member_filters: splitList(policy.member_filters),
      }),
    }),
    onSuccess: () => {
      setPolicyCredential(null)
      void client.invalidateQueries({ queryKey: ['byok'] })
      toast('BYOK routing policy updated')
    },
  })
  const disable = useMutation({
    mutationFn: (id: string) => api(`/byok/credentials/${id}`, { method: 'DELETE', query: { organization_id: scope.organizationID } }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['byok'] })
      toast('BYOK credential disabled')
    },
  })

  const editPolicy = (credential: BYOKCredential) => {
    setPolicyCredential(credential)
    setPolicy(policyFromCredential(credential))
  }

  return <div className="page-stack">
    <div className="page-header"><div><h1>Bring your own key</h1><p>Use organization-owned official Provider credentials as prioritized or fallback capacity.</p></div><Button variant="primary" disabled={!scope.projectID} onClick={() => { setPolicy(defaultPolicy); setOpen(true) }}><Plus size={14} />Add BYOK credential</Button></div>
    <Panel title="Ownership and capacity boundary" description="ModelDock never buys, sells, transfers, or shares Provider API keys or consumer accounts."><div className="inline-warning"><ShieldCheck size={16} />Filters scope where this key may run. The shared-capacity setting controls whether platform capacity may be used when the key does not match or is unavailable.</div></Panel>
    {result.isLoading && <Panel><Skeleton rows={4} /></Panel>}
    {result.isError && <ErrorState error={result.error} onRetry={() => void result.refetch()} />}
    {result.isSuccess && (result.data?.items.length || 0) === 0 && <Panel><EmptyState title="No BYOK credentials" description="Add an organization-owned official Provider API credential." /></Panel>}
    {(result.data?.items.length || 0) > 0 && <Panel><DataTable rows={result.data!.items} rowKey={(row) => row.id} columns={[
      { key: 'name', label: 'Credential', render: (row) => <div className="primary-cell"><strong>{row.name}</strong><small>{row.provider_name || row.provider_id}</small></div> },
      { key: 'secret', label: 'Secret', render: (row) => <code>••••{row.secret_last4}</code> },
      { key: 'priority', label: 'Capacity order', render: (row) => <StatusBadge value={row.byok_priority_section || 'PRIORITIZED'} /> },
      { key: 'fallback', label: 'Shared capacity', render: (row) => <StatusBadge value={row.shared_capacity_fallback || 'ALWAYS'} /> },
      { key: 'filters', label: 'Filters', render: (row) => <div className="primary-cell"><strong>{(row.model_filters || []).length} models</strong><small>{(row.api_key_filters || []).length} keys · {(row.member_filters || []).length} members</small></div> },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'confirmed', label: 'Ownership confirmed', render: (row) => <span>{formatDate(row.ownership_confirmed_at)}</span> },
      { key: 'actions', label: '', render: (row) => <div className="row-actions"><Button size="sm" onClick={() => editPolicy(row)}><Settings2 size={13} />Policy</Button><Button size="sm" variant="danger" disabled={disable.isPending || row.status === 'DISABLED'} onClick={() => disable.mutate(row.id)}><Trash2 size={13} />Disable</Button></div> },
    ]} /></Panel>}

    <Modal open={open} onClose={() => setOpen(false)} title="Add organization-owned Provider key" description="The secret is encrypted before persistence and is never shown again." wide footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="byok-create" pending={create.isPending} disabled={!confirmed || !scope.projectID}>Encrypt and save</SubmitButton></>}>
      <Form id="byok-create" className="form-grid" onSubmit={() => create.mutateAsync()}>
        <label><span>Provider *</span><select required value={providerID} onChange={(event) => setProviderID(event.target.value)}><option value="">Select Provider</option>{(providers.data?.items || []).map((provider) => <option key={provider.provider_id} value={provider.provider_id} disabled={!provider.enabled}>{provider.provider_name} ({provider.provider_slug})</option>)}</select></label>
        <label><span>Name *</span><input required value={name} onChange={(event) => setName(event.target.value)} placeholder="Organization production key" /></label>
        <label className="full-span"><span>Official API credential *</span><input required type="password" autoComplete="new-password" value={secret} onChange={(event) => setSecret(event.target.value)} /></label>
        <PolicyFields value={policy} onChange={setPolicy} />
        <label className="full-span"><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} /> I confirm this organization owns or is contractually authorized to use this credential, and will not use ModelDock to sell keys, share consumer accounts, automate Provider registration, bypass regions, or bypass safety mechanisms.</label>
        {create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Unable to save credential.'}</div>}
        <div className="one-time-label full-span"><KeyRound size={14} />Secret values are never returned by the API.</div>
      </Form>
    </Modal>

    <Modal open={Boolean(policyCredential)} onClose={() => setPolicyCredential(null)} title={`Routing policy · ${policyCredential?.name || ''}`} description="Changes affect new requests only." wide footer={<><Button onClick={() => setPolicyCredential(null)}>Cancel</Button><SubmitButton form="byok-policy" pending={updatePolicy.isPending}>Save policy</SubmitButton></>}>
      <Form id="byok-policy" className="form-grid" onSubmit={() => updatePolicy.mutateAsync()}><PolicyFields value={policy} onChange={setPolicy} />{updatePolicy.isError && <div className="form-error full-span">{updatePolicy.error instanceof Error ? updatePolicy.error.message : 'Unable to update policy.'}</div>}</Form>
    </Modal>
  </div>
}

function PolicyFields({ value, onChange }: { value: PolicyDraft; onChange: (value: PolicyDraft) => void }) {
  return <>
    <label><span>BYOK capacity order</span><select value={value.byok_priority_section} onChange={(event) => onChange({ ...value, byok_priority_section: event.target.value as PolicyDraft['byok_priority_section'] })}><option value="PRIORITIZED">Prioritized before shared capacity</option><option value="FALLBACK">Fallback after shared capacity</option></select></label>
    <label><span>Shared-capacity fallback</span><select value={value.shared_capacity_fallback} onChange={(event) => onChange({ ...value, shared_capacity_fallback: event.target.value as PolicyDraft['shared_capacity_fallback'] })}><option value="ALWAYS">Always allowed</option><option value="OUTSIDE_FILTERS">Only outside BYOK filters</option><option value="NEVER">Never use shared capacity</option></select></label>
    <label className="full-span"><span>Model filters</span><input value={value.model_filters} onChange={(event) => onChange({ ...value, model_filters: event.target.value })} placeholder="gpt-4.1, claude-3.7-sonnet" /><small>Comma-separated requested model aliases. Empty matches every model.</small></label>
    <label><span>API Key filters</span><input value={value.api_key_filters} onChange={(event) => onChange({ ...value, api_key_filters: event.target.value })} placeholder="API Key IDs" /></label>
    <label><span>Member filters</span><input value={value.member_filters} onChange={(event) => onChange({ ...value, member_filters: event.target.value })} placeholder="User IDs" /></label>
  </>
}
