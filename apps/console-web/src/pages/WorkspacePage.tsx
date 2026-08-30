import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Building2, KeyRound, Network, Save, ShieldCheck, TestTube2 } from 'lucide-react'
import { api } from '../lib/api'
import { useProjectScope } from '../lib/project-scope'
import { Button, ErrorState, Form, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type ProviderPolicy = {
  order?: string[]
  only?: string[]
  ignore?: string[]
  allow_fallbacks?: boolean
  require_parameters?: boolean
  data_collection?: 'allow' | 'deny' | ''
  zdr?: boolean
  quantizations?: string[]
  sort?: 'price' | 'latency' | 'throughput' | ''
  required_capabilities?: string[]
  processing_regions?: string[]
  use_shared_capacity?: boolean
}

type WorkspaceSettings = {
  project_id: string
  default_provider_policy: ProviderPolicy
  privacy_policy: Record<string, unknown>
  observability_config: Record<string, unknown>
  include_byok_in_budgets: boolean
  free_daily_request_limit: number
  free_daily_token_limit: number
  allowed_processing_regions: string[]
}

type WorkspaceDraft = {
  order: string
  only: string
  ignore: string
  sort: ProviderPolicy['sort']
  data_collection: ProviderPolicy['data_collection']
  quantizations: string
  required_capabilities: string
  processing_regions: string
  allow_fallbacks: boolean
  require_parameters: boolean
  zdr: boolean
  use_shared_capacity: boolean
  include_byok_in_budgets: boolean
  free_daily_request_limit: number
  free_daily_token_limit: number
}

type IdentityConnection = {
  id?: string
  organization_id: string
  issuer_url: string
  client_id: string
  has_client_secret: boolean
  has_scim_token: boolean
  allowed_domains: string[]
  sso_enabled: boolean
  scim_enabled: boolean
  enforce_sso: boolean
  status: 'ACTIVE' | 'DISABLED'
  metadata: Record<string, unknown>
}

type IdentityDraft = IdentityConnection & { client_secret: string; rotate_scim_token: boolean }
type IdentitySaveResponse = { connection: IdentityConnection; scim_base_path: string; scim_token?: string }

const emptyWorkspace: WorkspaceDraft = {
  order: '', only: '', ignore: '', sort: '', data_collection: 'deny', quantizations: '', required_capabilities: '', processing_regions: '',
  allow_fallbacks: true, require_parameters: false, zdr: false, use_shared_capacity: true, include_byok_in_budgets: false,
  free_daily_request_limit: 0, free_daily_token_limit: 0,
}

const emptyIdentity: IdentityDraft = {
  organization_id: '', issuer_url: '', client_id: '', client_secret: '', has_client_secret: false, has_scim_token: false,
  allowed_domains: [], sso_enabled: false, scim_enabled: false, enforce_sso: false, status: 'DISABLED', metadata: {}, rotate_scim_token: false,
}

function list(value: string) {
  return [...new Set(value.split(',').map((item) => item.trim().toLowerCase()).filter(Boolean))]
}

function workspaceDraft(settings: WorkspaceSettings): WorkspaceDraft {
  const policy = settings.default_provider_policy || {}
  return {
    order: (policy.order || []).join(', '), only: (policy.only || []).join(', '), ignore: (policy.ignore || []).join(', '),
    sort: policy.sort || '', data_collection: policy.data_collection || 'deny', quantizations: (policy.quantizations || []).join(', '),
    required_capabilities: (policy.required_capabilities || []).join(', '),
    processing_regions: (settings.allowed_processing_regions || policy.processing_regions || []).join(', '),
    allow_fallbacks: policy.allow_fallbacks !== false, require_parameters: Boolean(policy.require_parameters), zdr: Boolean(policy.zdr),
    use_shared_capacity: policy.use_shared_capacity !== false, include_byok_in_budgets: settings.include_byok_in_budgets,
    free_daily_request_limit: settings.free_daily_request_limit, free_daily_token_limit: settings.free_daily_token_limit,
  }
}

export function WorkspacePage() {
  const scope = useProjectScope()
  const queryClient = useQueryClient()
  const toast = useToast()
  const [workspace, setWorkspace] = useState<WorkspaceDraft>(emptyWorkspace)
  const [identity, setIdentity] = useState<IdentityDraft>(emptyIdentity)
  const [scimToken, setScimToken] = useState('')
  const [identityTest, setIdentityTest] = useState<Record<string, unknown> | null>(null)

  const settings = useQuery({
    queryKey: ['workspace-settings', scope.projectID],
    enabled: Boolean(scope.projectID),
    queryFn: () => api<WorkspaceSettings>(`/projects/${scope.projectID}/workspace-settings`),
  })
  const connection = useQuery({
    queryKey: ['enterprise-identity', scope.organizationID],
    enabled: Boolean(scope.organizationID),
    queryFn: () => api<IdentityConnection>(`/organizations/${scope.organizationID}/enterprise-identity`),
  })

  useEffect(() => { if (settings.data) setWorkspace(workspaceDraft(settings.data)) }, [settings.data])
  useEffect(() => {
    if (connection.data) setIdentity({ ...emptyIdentity, ...connection.data, client_secret: '', rotate_scim_token: false })
  }, [connection.data])

  const saveWorkspace = useMutation({
    mutationFn: () => api<WorkspaceSettings>(`/projects/${scope.projectID}/workspace-settings`, {
      method: 'PUT',
      body: JSON.stringify({
        default_provider_policy: {
          order: list(workspace.order), only: list(workspace.only), ignore: list(workspace.ignore), sort: workspace.sort,
          allow_fallbacks: workspace.allow_fallbacks, require_parameters: workspace.require_parameters,
          data_collection: workspace.data_collection, zdr: workspace.zdr, quantizations: list(workspace.quantizations),
          required_capabilities: list(workspace.required_capabilities), processing_regions: list(workspace.processing_regions),
          use_shared_capacity: workspace.use_shared_capacity,
        },
        privacy_policy: settings.data?.privacy_policy || {}, observability_config: settings.data?.observability_config || {},
        include_byok_in_budgets: workspace.include_byok_in_budgets,
        free_daily_request_limit: workspace.free_daily_request_limit,
        free_daily_token_limit: workspace.free_daily_token_limit,
        allowed_processing_regions: list(workspace.processing_regions),
      }),
    }),
    onSuccess: (value) => {
      queryClient.setQueryData(['workspace-settings', scope.projectID], value)
      toast('Workspace routing policy saved')
    },
  })

  const saveIdentity = useMutation({
    mutationFn: () => api<IdentitySaveResponse>(`/organizations/${scope.organizationID}/enterprise-identity`, {
      method: 'PUT',
      body: JSON.stringify({
        issuer_url: identity.issuer_url, client_id: identity.client_id, client_secret: identity.client_secret,
        allowed_domains: identity.allowed_domains, sso_enabled: identity.sso_enabled, scim_enabled: identity.scim_enabled,
        enforce_sso: identity.enforce_sso, status: identity.status, metadata: identity.metadata,
        rotate_scim_token: identity.rotate_scim_token,
      }),
    }),
    onSuccess: (value) => {
      setIdentity({ ...emptyIdentity, ...value.connection, client_secret: '', rotate_scim_token: false })
      setScimToken(value.scim_token || '')
      queryClient.setQueryData(['enterprise-identity', scope.organizationID], value.connection)
      toast(value.scim_token ? 'Identity settings saved; copy the SCIM token now' : 'Identity settings saved')
    },
  })
  const testIdentity = useMutation({
    mutationFn: () => api<Record<string, unknown>>(`/organizations/${scope.organizationID}/enterprise-identity/test`, { method: 'POST' }),
    onSuccess: (value) => { setIdentityTest(value); toast('OIDC discovery succeeded') },
  })

  if (settings.isLoading || connection.isLoading) return <div className="page-stack"><Panel><Skeleton rows={8} /></Panel></div>
  if (settings.isError) return <ErrorState error={settings.error} onRetry={() => void settings.refetch()} />
  if (connection.isError) return <ErrorState error={connection.error} onRetry={() => void connection.refetch()} />

  return <div className="page-stack">
    <div className="page-header"><div><h1>Workspace policy</h1><p>One place for Provider routing, free-model guardrails, BYOK budget treatment, regions, SSO, and SCIM.</p></div><StatusBadge value={identity.status} /></div>

    <Panel title="Default Provider routing" description="Request-level provider settings may narrow privacy and region constraints, but cannot relax these Workspace defaults.">
      <Form className="form-grid panel-pad" onSubmit={() => saveWorkspace.mutateAsync()}>
        <label><span>Optimization strategy</span><select value={workspace.sort} onChange={(event) => setWorkspace({ ...workspace, sort: event.target.value as WorkspaceDraft['sort'] })}><option value="">Balanced routing rule</option><option value="price">Lowest price</option><option value="latency">Lowest measured latency</option><option value="throughput">Highest measured throughput</option></select></label>
        <label><span>Provider order</span><input value={workspace.order} onChange={(event) => setWorkspace({ ...workspace, order: event.target.value })} placeholder="anthropic, openai, google" /></label>
        <label><span>Only Providers</span><input value={workspace.only} onChange={(event) => setWorkspace({ ...workspace, only: event.target.value })} placeholder="Optional allowlist" /></label>
        <label><span>Ignore Providers</span><input value={workspace.ignore} onChange={(event) => setWorkspace({ ...workspace, ignore: event.target.value })} placeholder="Optional denylist" /></label>
        <label><span>Allowed processing regions</span><input value={workspace.processing_regions} onChange={(event) => setWorkspace({ ...workspace, processing_regions: event.target.value })} placeholder="gb, de, us" /></label>
        <label><span>Required capabilities</span><input value={workspace.required_capabilities} onChange={(event) => setWorkspace({ ...workspace, required_capabilities: event.target.value })} placeholder="vision, tools, json" /></label>
        <label><span>Quantizations</span><input value={workspace.quantizations} onChange={(event) => setWorkspace({ ...workspace, quantizations: event.target.value })} placeholder="fp16, bf16" /></label>
        <label><span>Data collection</span><select value={workspace.data_collection} onChange={(event) => setWorkspace({ ...workspace, data_collection: event.target.value as WorkspaceDraft['data_collection'] })}><option value="deny">Deny collection</option><option value="allow">Allow under Provider terms</option></select></label>
        <label><input type="checkbox" checked={workspace.allow_fallbacks} onChange={(event) => setWorkspace({ ...workspace, allow_fallbacks: event.target.checked })} /> Permit model and Provider fallback</label>
        <label><input type="checkbox" checked={workspace.require_parameters} onChange={(event) => setWorkspace({ ...workspace, require_parameters: event.target.checked })} /> Require Provider parameter compatibility</label>
        <label><input type="checkbox" checked={workspace.zdr} onChange={(event) => setWorkspace({ ...workspace, zdr: event.target.checked })} /> Require zero-data-retention capability</label>
        <label><input type="checkbox" checked={workspace.use_shared_capacity} onChange={(event) => setWorkspace({ ...workspace, use_shared_capacity: event.target.checked })} /> Permit platform shared capacity</label>
        <label><input type="checkbox" checked={workspace.include_byok_in_budgets} onChange={(event) => setWorkspace({ ...workspace, include_byok_in_budgets: event.target.checked })} /> Include BYOK catalog-price shadow spend in budgets</label>
        <span />
        <label><span>Free requests / API Key / day</span><input type="number" min={0} value={workspace.free_daily_request_limit} onChange={(event) => setWorkspace({ ...workspace, free_daily_request_limit: Number(event.target.value) })} /></label>
        <label><span>Free tokens / API Key / day</span><input type="number" min={0} value={workspace.free_daily_token_limit} onChange={(event) => setWorkspace({ ...workspace, free_daily_token_limit: Number(event.target.value) })} /></label>
        <div className="inline-note full-span"><Network size={15} /> Use model <code>auto:free</code> to route only to zero-price catalog entries. A zero limit disables free routing; it never means unlimited.</div>
        {saveWorkspace.isError && <div className="form-error full-span">{saveWorkspace.error instanceof Error ? saveWorkspace.error.message : 'Unable to save Workspace policy.'}</div>}
        <SubmitButton pending={saveWorkspace.isPending}><Save size={14} />Save Workspace policy</SubmitButton>
      </Form>
    </Panel>

    <Panel title="Enterprise identity" description="OIDC client secrets are encrypted; SCIM bearer tokens are hashed and shown only once when rotated.">
      <Form className="form-grid panel-pad" onSubmit={() => saveIdentity.mutateAsync()}>
        <label><span>Connection status</span><select value={identity.status} onChange={(event) => setIdentity({ ...identity, status: event.target.value as IdentityDraft['status'] })}><option value="DISABLED">Disabled</option><option value="ACTIVE">Active</option></select></label>
        <label><span>Allowed email domains</span><input value={identity.allowed_domains.join(', ')} onChange={(event) => setIdentity({ ...identity, allowed_domains: list(event.target.value) })} placeholder="example.com" /></label>
        <label className="full-span"><span>OIDC issuer URL</span><input type="url" value={identity.issuer_url} onChange={(event) => setIdentity({ ...identity, issuer_url: event.target.value })} placeholder="https://id.example.com" /></label>
        <label><span>OIDC client ID</span><input value={identity.client_id} onChange={(event) => setIdentity({ ...identity, client_id: event.target.value })} /></label>
        <label><span>OIDC client secret</span><input type="password" autoComplete="new-password" value={identity.client_secret} onChange={(event) => setIdentity({ ...identity, client_secret: event.target.value })} placeholder={identity.has_client_secret ? 'Stored — enter to rotate' : 'Required before enabling SSO'} /></label>
        <label><input type="checkbox" checked={identity.sso_enabled} onChange={(event) => setIdentity({ ...identity, sso_enabled: event.target.checked, enforce_sso: event.target.checked ? identity.enforce_sso : false })} /> Enable OIDC SSO</label>
        <label><input type="checkbox" checked={identity.enforce_sso} onChange={(event) => setIdentity({ ...identity, enforce_sso: event.target.checked })} disabled={!identity.sso_enabled} /> Enforce SSO for this organization</label>
        <label><input type="checkbox" checked={identity.scim_enabled} onChange={(event) => setIdentity({ ...identity, scim_enabled: event.target.checked })} /> Enable SCIM 2.0</label>
        <label><input type="checkbox" checked={identity.rotate_scim_token} onChange={(event) => setIdentity({ ...identity, rotate_scim_token: event.target.checked })} /> Generate / rotate SCIM token</label>
        <div className="inline-warning full-span"><ShieldCheck size={15} /> SCIM Users map to organization membership and Groups map to Teams. Deprovisioning never deletes a user from another organization and cannot remove the last owner.</div>
        {scimToken && <div className="one-time-label full-span"><KeyRound size={14} /><span>Copy this SCIM token now: <code>{scimToken}</code></span></div>}
        {saveIdentity.isError && <div className="form-error full-span">{saveIdentity.error instanceof Error ? saveIdentity.error.message : 'Unable to save identity settings.'}</div>}
        <div className="row-actions"><SubmitButton pending={saveIdentity.isPending}><Building2 size={14} />Save identity</SubmitButton><Button type="button" disabled={!identity.has_client_secret || !identity.issuer_url || testIdentity.isPending} onClick={() => testIdentity.mutate()}><TestTube2 size={14} />Test discovery</Button></div>
        {testIdentity.isError && <div className="form-error full-span">{testIdentity.error instanceof Error ? testIdentity.error.message : 'OIDC discovery failed.'}</div>}
        {identityTest && <div className="inline-note full-span">OIDC discovery verified for <code>{String(identityTest.issuer || identity.issuer_url)}</code>.</div>}
      </Form>
    </Panel>
  </div>
}
