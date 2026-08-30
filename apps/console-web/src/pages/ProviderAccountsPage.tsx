import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link2, RefreshCw, ShieldCheck } from 'lucide-react'
import { Link } from 'react-router-dom'
import { api, asPage, formatDate, formatMoney } from '../lib/api'
import { useProjectScope } from '../lib/project-scope'
import { Badge, Button, DataTable, EmptyState, ErrorState, Panel, Skeleton, StatusBadge, useToast } from '../components/ui'

type Capability = { provider_id: string; provider_name: string; provider_type: string; mode: string; enabled: boolean; supports_automatic_binding: boolean; supports_automatic_credit: boolean; reason?: string }
type Binding = { id: string; provider_id: string; provider_name: string; provisioning_mode: string; status: string; external_project_id?: string; external_account_id?: string; credential_id?: string; allocated_amount: string; currency?: string; created_at: string; last_synced_at?: string }
type Job = { id: string; provider_name: string; operation: string; status: string; amount?: string; currency?: string; error_code?: string; created_at: string }

export function ProviderAccountsPage() {
  const scope = useProjectScope()
  const client = useQueryClient()
  const toast = useToast()
  const capabilities = useQuery({ queryKey: ['provider-provisioning-capabilities'], queryFn: () => api<unknown>('/provider-provisioning/capabilities').then(asPage<Capability>) })
  const bindings = useQuery({ queryKey: ['console-provider-accounts', scope.organizationID], queryFn: () => api<unknown>(`/organizations/${scope.organizationID}/provider-accounts`).then(asPage<Binding>), enabled: Boolean(scope.organizationID), refetchInterval: 15_000 })
  const jobs = useQuery({ queryKey: ['console-provider-provisioning-jobs', scope.organizationID], queryFn: () => api<unknown>(`/organizations/${scope.organizationID}/provider-provisioning/jobs`).then(asPage<Job>), enabled: Boolean(scope.organizationID), refetchInterval: 10_000 })
  const connect = useMutation({
    mutationFn: (capability: Capability) => api(`/organizations/${scope.organizationID}/provider-accounts`, { method: 'POST', headers: { 'Idempotency-Key': `provider-binding-${crypto.randomUUID()}` }, body: JSON.stringify({ provider_id: capability.provider_id, provisioning_mode: capability.mode, automatic: true }) }),
    onSuccess: () => { toast('Official Provider binding queued'); refresh() },
  })
  const refresh = () => { void client.invalidateQueries({ queryKey: ['provider-provisioning-capabilities'] }); void client.invalidateQueries({ queryKey: ['console-provider-accounts', scope.organizationID] }); void client.invalidateQueries({ queryKey: ['console-provider-provisioning-jobs', scope.organizationID] }) }

  return <div className="page-stack">
    <div className="page-header"><div><h1>Provider accounts</h1><p>View the official enterprise projects, reviewed accounts, and BYOK credentials bound to your RelayDock account.</p></div><Button onClick={refresh}><RefreshCw size={14} />Refresh</Button></div>
    <div className="security-card"><ShieldCheck size={20} /><div><strong>Account creation follows Provider contracts</strong><p>No consumer signup automation, CAPTCHA bypass, temporary mail, proxy rotation, or free-trial farming is performed.</p></div><Badge tone="success">Audited</Badge></div>
    <Panel title="Available binding methods" description="Automatic binding and upstream credit are separate capabilities.">
      {capabilities.isLoading && <Skeleton rows={4} />}{capabilities.isError && <ErrorState error={capabilities.error} onRetry={() => capabilities.refetch()} />}
      {capabilities.isSuccess && <DataTable rows={capabilities.data.items} rowKey={(row) => row.provider_id} columns={[
        { key: 'provider', label: 'Provider', render: (row) => <div className="primary-cell"><strong>{row.provider_name}</strong><small>{row.provider_type}</small></div> },
        { key: 'mode', label: 'Mode', render: (row) => <Badge tone="violet">{row.mode}</Badge> },
        { key: 'binding', label: 'Auto binding', render: (row) => <StatusBadge value={row.enabled && row.supports_automatic_binding ? 'enabled' : 'manual'} /> },
        { key: 'credit', label: 'Upstream credit', render: (row) => <StatusBadge value={row.enabled && row.supports_automatic_credit ? 'enabled' : 'unsupported'} /> },
        { key: 'action', label: '', render: (row) => row.enabled && row.supports_automatic_binding ? <Button size="sm" disabled={!scope.organizationID || connect.isPending} onClick={() => connect.mutate(row)}><Link2 size={13} />Connect</Button> : <Link className="button button-default button-sm" to="/console/byok">Use BYOK</Link> },
      ]} />}
    </Panel>
    <Panel title="Your bindings" description="Upstream secrets are never returned to the browser.">
      {bindings.isLoading && <Skeleton rows={4} />}{bindings.isError && <ErrorState error={bindings.error} onRetry={() => bindings.refetch()} />}
      {bindings.isSuccess && bindings.data.items.length === 0 && <EmptyState title="No Provider bindings" />}
      {!!bindings.data?.items.length && <DataTable rows={bindings.data.items} rowKey={(row) => row.id} columns={[
        { key: 'provider', label: 'Provider', render: (row) => <div className="primary-cell"><strong>{row.provider_name}</strong><small>{row.provisioning_mode}</small></div> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'external', label: 'Project / account', render: (row) => <div className="primary-cell"><strong>{row.external_project_id || '—'}</strong><small>{row.external_account_id || '—'}</small></div> },
        { key: 'credential', label: 'Credential', render: (row) => row.credential_id ? <Badge tone="success">Encrypted pool</Badge> : '—' },
        { key: 'allocation', label: 'Upstream allocation', render: (row) => formatMoney(row.allocated_amount, row.currency || 'USD') },
        { key: 'sync', label: 'Last sync', render: (row) => formatDate(row.last_synced_at || row.created_at) },
      ]} />}
    </Panel>
    <Panel title="Provisioning history" description="Payment replay and job retry reuse the same idempotency scope.">
      {jobs.isLoading && <Skeleton rows={3} />}{jobs.isError && <ErrorState error={jobs.error} onRetry={() => jobs.refetch()} />}
      {jobs.isSuccess && jobs.data.items.length === 0 && <EmptyState title="No provisioning jobs" />}
      {!!jobs.data?.items.length && <DataTable rows={jobs.data.items} rowKey={(row) => row.id} columns={[
        { key: 'operation', label: 'Operation', render: (row) => <div className="primary-cell"><strong>{row.provider_name}</strong><small>{row.operation}</small></div> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'amount', label: 'Amount', render: (row) => row.amount ? formatMoney(row.amount, row.currency) : '—' },
        { key: 'error', label: 'Result', render: (row) => row.error_code || '—' },
        { key: 'created', label: 'Created', render: (row) => formatDate(row.created_at) },
      ]} />}
    </Panel>
  </div>
}
