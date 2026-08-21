import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, asPage, formatDate } from '../lib/api'
import { useProjectScope } from '../lib/project-scope'
import { Button, DataTable, EmptyState, ErrorState, Form, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type Entitlement = { entitlement_key: string; integer_value?: number; boolean_value?: boolean; string_value?: string }
type PlanVersion = { id: string; version: number; status: string; subscription_fee: string; currency: string; billing_interval: string; token_billing_mode: string; entitlements: Entitlement[] }
type Plan = { id: string; slug: string; name: string; description: string; current_version_id?: string }
type SubscriptionView = { subscription: Record<string, unknown>; entitlements: Record<string, unknown>; token_billing_mode: string }

export function SubscriptionPage() {
  const scope = useProjectScope()
  const [changeOpen, setChangeOpen] = useState(false)
  const [cancelOpen, setCancelOpen] = useState(false)
  const [target, setTarget] = useState('')
  const [mode, setMode] = useState('IMMEDIATE')
  const client = useQueryClient()
  const toast = useToast()
  const plans = useQuery({ queryKey: ['console-subscription-plans'], queryFn: () => api<unknown>('/subscription-plans').then(asPage<Plan>) })
  const planVersions = useQuery({
    queryKey: ['console-subscription-plan-versions', plans.data?.items.map((plan) => plan.current_version_id).join(',')],
    queryFn: async () => {
      const values = await Promise.all((plans.data?.items || []).filter((plan) => plan.current_version_id).map(async (plan) => {
        const response = await api<unknown>(`/subscription-plans/${plan.id}/versions`).then(asPage<PlanVersion>)
        return { plan, version: response.items.find((item) => item.id === plan.current_version_id) }
      }))
      return values.filter((value): value is { plan: Plan; version: PlanVersion } => Boolean(value.version))
    },
    enabled: Boolean(plans.data?.items.length),
  })
  const current = useQuery({ queryKey: ['console-subscription', scope.organizationID], queryFn: () => api<SubscriptionView>(`/organizations/${scope.organizationID}/subscription`), enabled: Boolean(scope.organizationID) })
  const invoices = useQuery({ queryKey: ['console-subscription-invoices', scope.organizationID], queryFn: () => api<unknown>(`/organizations/${scope.organizationID}/subscription-invoices`).then(asPage<Record<string, unknown>>), enabled: Boolean(scope.organizationID) })
  const refresh = () => { void client.invalidateQueries({ queryKey: ['console-subscription', scope.organizationID] }); void client.invalidateQueries({ queryKey: ['console-subscription-invoices', scope.organizationID] }) }
  const change = useMutation({
    mutationFn: () => api(`/organizations/${scope.organizationID}/subscription/change`, { method: 'POST', body: JSON.stringify({ plan_version_id: target, mode, use_trial: true, idempotency_key: crypto.randomUUID() }) }),
    onSuccess: () => { setChangeOpen(false); refresh(); toast(mode === 'IMMEDIATE' ? 'Subscription changed' : 'Plan change scheduled') },
  })
  const cancel = useMutation({
    mutationFn: () => api(`/organizations/${scope.organizationID}/subscription/cancel`, { method: 'POST', body: JSON.stringify({ mode, idempotency_key: crypto.randomUUID() }) }),
    onSuccess: () => { setCancelOpen(false); refresh(); toast(mode === 'IMMEDIATE' ? 'Subscription canceled; Free entitlements are active' : 'Cancellation scheduled') },
  })

  if (!scope.organizationID && !scope.loading) return <EmptyState title="Select an organization" description="Subscription lifecycle and invoices are organization scoped." />
  return <div className="page-stack">
    <div className="page-header"><div><h1>Subscription</h1><p>Manage feature entitlements. Token usage is always billed separately at the configured metered price.</p></div><div className="header-actions"><Button onClick={() => setCancelOpen(true)} disabled={!current.data}>Cancel</Button><Button variant="primary" onClick={() => setChangeOpen(true)} disabled={!planVersions.data?.length}>Change plan</Button></div></div>
    <div className="pricing-disclaimer"><span>Plan fees and Token charges are separate reconciliation streams. No plan provides unlimited Token usage.</span></div>
    {current.isLoading && <Panel><Skeleton rows={7} /></Panel>}
    {current.isError && <ErrorState error={current.error} onRetry={() => current.refetch()} />}
    {current.data && <><div className="metric-grid"><div className="metric"><span>Current plan</span><strong>{String(current.data.subscription.plan_name || current.data.subscription.plan_slug)}</strong><small>Version {String(current.data.subscription.plan_version)}</small></div><div className="metric"><span>Status</span><strong><StatusBadge value={current.data.subscription.status} /></strong><small>Ends {formatDate(current.data.subscription.current_period_end)}</small></div><div className="metric"><span>Token billing</span><strong>Metered separately</strong><small>{current.data.token_billing_mode}</small></div></div><Panel title="Effective entitlements" description="The server enforces these limits even for direct API calls."><div className="panel-pad detail-list">{Object.entries(current.data.entitlements).filter(([key]) => !['organization_id', 'subscription_id', 'plan_version_id', 'plan_slug', 'subscription_status', 'token_billing_mode'].includes(key)).map(([key, value]) => <div key={key}><span>{key.replaceAll('_', ' ')}</span><strong>{typeof value === 'boolean' ? (value ? 'Included' : 'Not included') : String(value)}</strong></div>)}</div></Panel></>}
    <Panel title="Subscription invoices" description="These invoices exclude recharge orders and Token usage journals.">{invoices.isLoading && <Skeleton rows={5} />}{invoices.isError && <ErrorState error={invoices.error} onRetry={() => invoices.refetch()} />}{invoices.isSuccess && !invoices.data.items.length && <EmptyState title="No subscription invoices" description="Free and trial periods do not create a paid invoice." />}{!!invoices.data?.items.length && <DataTable rows={invoices.data.items} rowKey={(row) => String(row.id)} columns={[
      { key: 'invoice', label: 'Invoice', render: (row) => <div className="primary-cell"><strong>{String(row.invoice_number)}</strong><small>{String(row.invoice_type)}</small></div> },
      { key: 'amount', label: 'Subscription fee', render: (row) => <strong>{String(row.currency)} {String(row.total_amount)}</strong> },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'period', label: 'Period', render: (row) => `${formatDate(row.period_start)} – ${formatDate(row.period_end)}` },
    ]} />}</Panel>
    <Modal open={changeOpen} onClose={() => setChangeOpen(false)} title="Change subscription" description="The selected frozen plan version will be snapshotted on every invoice." footer={<><Button onClick={() => setChangeOpen(false)}>Cancel</Button><SubmitButton form="change-subscription" pending={change.isPending}>Confirm change</SubmitButton></>} wide><Form id="change-subscription" className="form-grid" onSubmit={() => change.mutateAsync()}><label className="full-span"><span>Plan version</span><select required value={target} onChange={(event) => setTarget(event.target.value)}><option value="">Select a plan</option>{planVersions.data?.map(({ plan, version }) => <option key={version.id} value={version.id}>{plan.name} — {version.currency} {version.subscription_fee}/{version.billing_interval.toLowerCase()}</option>)}</select></label><label><span>Timing</span><select value={mode} onChange={(event) => setMode(event.target.value)}><option value="IMMEDIATE">Immediately</option><option value="PERIOD_END">At period end</option></select></label><div className="inline-note full-span">Token usage remains METERED_SEPARATE after this change.</div>{change.isError && <div className="form-error full-span">{change.error instanceof Error ? change.error.message : 'Plan change failed.'}</div>}</Form></Modal>
    <Modal open={cancelOpen} onClose={() => setCancelOpen(false)} title="Cancel subscription" description="Existing keys, members, logs, and webhooks are retained. Server-side creation and request limits fall back to Free." footer={<><Button onClick={() => setCancelOpen(false)}>Keep subscription</Button><SubmitButton form="cancel-subscription" pending={cancel.isPending}>Confirm cancellation</SubmitButton></>}><Form id="cancel-subscription" className="form-grid" onSubmit={() => cancel.mutateAsync()}><label className="full-span"><span>Timing</span><select value={mode} onChange={(event) => setMode(event.target.value)}><option value="PERIOD_END">At period end</option><option value="IMMEDIATE">Immediately</option></select></label>{cancel.isError && <div className="form-error full-span">{cancel.error instanceof Error ? cancel.error.message : 'Cancellation failed.'}</div>}</Form></Modal>
  </div>
}
