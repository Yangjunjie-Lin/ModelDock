import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, asPage, formatDate } from '../lib/api'
import type { Row } from '../lib/types'
import { Badge, Button, DataTable, EmptyState, ErrorState, Panel, Skeleton, StatusBadge } from '../components/ui'

type Plan = Row & { id: string; slug: string; name: string; description?: string; current_version_id?: string }
type Version = Row & { id: string; version: number; status: string; subscription_fee: string; currency: string; billing_interval: string; token_billing_mode: string; entitlements?: Array<Record<string, unknown>> }

export function SubscriptionsPage() {
  const [selectedPlan, setSelectedPlan] = useState('')
  const [organizationID, setOrganizationID] = useState('')
  const plans = useQuery({ queryKey: ['subscription-plans'], queryFn: () => api<unknown>('/subscription-plans').then(asPage<Plan>) })
  const activePlan = selectedPlan || plans.data?.items[0]?.id || ''
  const versions = useQuery({ queryKey: ['plan-versions', activePlan], queryFn: () => api<unknown>(`/subscription-plans/${activePlan}/versions`).then(asPage<Version>), enabled: Boolean(activePlan) })
  const subscription = useQuery({ queryKey: ['organization-subscription', organizationID], queryFn: () => api<Record<string, unknown>>(`/organizations/${organizationID}/subscription`), enabled: Boolean(organizationID) })
  const invoices = useQuery({ queryKey: ['subscription-invoices', organizationID], queryFn: () => api<unknown>(`/organizations/${organizationID}/subscription-invoices`).then(asPage<Row>), enabled: Boolean(organizationID) })
  const selected = useMemo(() => plans.data?.items.find((plan) => plan.id === activePlan), [activePlan, plans.data])

  return <div className="page-stack">
    <div className="page-header"><div><h1>Subscriptions</h1><p>Manage immutable plan versions, organization lifecycle, and subscription invoices independently from Token usage billing.</p></div></div>
    <div className="pricing-disclaimer"><span>Every plan uses <strong>METERED_SEPARATE</strong>: subscription fees never include unlimited Token usage and never credit the recharge wallet.</span></div>
    <Panel title="Plan templates" description="Free, Developer, Team, and Enterprise are seeded as configurable frozen templates.">
      {plans.isLoading && <Skeleton rows={4} />}
      {plans.isError && <ErrorState error={plans.error} onRetry={() => plans.refetch()} />}
      {!!plans.data?.items.length && <DataTable rows={plans.data.items} rowKey={(row) => row.id} columns={[
        { key: 'plan', label: 'Plan', render: (row) => <div className="primary-cell"><strong>{row.name}</strong><small>{String(row.description || row.slug)}</small></div> },
        { key: 'kind', label: 'Kind', render: (row) => <Badge tone={row.plan_kind === 'ENTERPRISE_CONTRACT' ? 'violet' : 'info'}>{String(row.plan_kind)}</Badge> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.enabled ? 'active' : 'disabled'} /> },
        { key: 'action', label: '', render: (row) => <Button size="sm" variant={activePlan === row.id ? 'primary' : 'default'} onClick={() => setSelectedPlan(row.id)}>Versions</Button> },
      ]} />}
    </Panel>
    {selected && <Panel title={`${selected.name} versions`} description="A frozen version cannot be edited; publish a new version for future subscriptions.">
      {versions.isLoading && <Skeleton rows={5} />}
      {versions.isError && <ErrorState error={versions.error} onRetry={() => versions.refetch()} />}
      {versions.isSuccess && !versions.data.items.length && <EmptyState title="No plan versions" />}
      {!!versions.data?.items.length && <DataTable rows={versions.data.items} rowKey={(row) => row.id} columns={[
        { key: 'version', label: 'Version', render: (row) => <strong>v{row.version}</strong> },
        { key: 'fee', label: 'Subscription fee', render: (row) => <strong>{row.currency} {row.subscription_fee} / {row.billing_interval.toLowerCase()}</strong> },
        { key: 'token', label: 'Token billing', render: (row) => <Badge tone="warning">{row.token_billing_mode}</Badge> },
        { key: 'entitlements', label: 'Entitlements', render: (row) => `${row.entitlements?.length || 0} configured` },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      ]} />}
    </Panel>}
    <Panel title="Organization reconciliation" description="Enter an organization UUID to compare its subscription stream without mixing recharge or Token charges.">
      <div className="resource-toolbar"><label className="search-input"><input value={organizationID} onChange={(event) => setOrganizationID(event.target.value.trim())} placeholder="Organization UUID" /></label></div>
      {subscription.isLoading && <Skeleton rows={4} />}
      {subscription.isError && <ErrorState error={subscription.error} onRetry={() => subscription.refetch()} />}
      {subscription.data && <div className="panel-pad detail-list">{Object.entries((subscription.data.subscription || {}) as Record<string, unknown>).filter(([key]) => ['plan_name', 'plan_version', 'status', 'current_period_start', 'current_period_end', 'cancel_at_period_end'].includes(key)).map(([key, value]) => <div key={key}><span>{key.replaceAll('_', ' ')}</span><strong>{key.includes('period_') ? formatDate(value) : String(value)}</strong></div>)}</div>}
      {!!invoices.data?.items.length && <DataTable rows={invoices.data.items} rowKey={(row) => String(row.id)} columns={[
        { key: 'invoice', label: 'Invoice', render: (row) => <div className="primary-cell"><strong>{String(row.invoice_number)}</strong><small>{String(row.invoice_type)}</small></div> },
        { key: 'amount', label: 'Subscription amount', render: (row) => <strong>{String(row.currency)} {String(row.total_amount)}</strong> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'journal', label: 'Subscription journal', render: (row) => row.ledger_journal_id ? <code>{String(row.ledger_journal_id)}</code> : 'Awaiting payment' },
        { key: 'created', label: 'Created', render: (row) => formatDate(row.created_at) },
      ]} />}
    </Panel>
  </div>
}
