import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { CreditCard, RefreshCw } from 'lucide-react'
import { api, asPage, formatDate, formatMoney } from '../lib/api'
import { approvedPaymentChannelFee, commercialTermsApproved, paymentProviderRuntimeAdmitted, publicApi, type PublicConfig, type PublicPricing } from '../lib/public-api'
import { usePublicSettings } from '../lib/public-settings'
import { useProjectScope } from '../lib/project-scope'
import { Badge, Button, DataTable, EmptyState, ErrorState, Form, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type Provider = { name: string; enabled?: boolean; contract_status: string; production_ready: boolean; allowed_regions: string[] }
type ProvisioningCapability = { provider_id: string; provider_name: string; mode: string; enabled: boolean; supports_automatic_binding: boolean; supports_automatic_credit: boolean; reason?: string }
type RechargeOrder = {
  id: string; platform_order_no: string; provider_order_no?: string; payment_provider: string; status: string
  amount: string; currency: string; region: string; expires_at: string; created_at: string
  target_provider_id?: string; target_provisioning_mode?: string
}
type CreateResponse = { order: RechargeOrder; payment: { instructions?: Record<string, unknown> } }

export function PaymentsPage() {
  const scope = useProjectScope()
  const settings = usePublicSettings()
  const client = useQueryClient()
  const toast = useToast()
  const [amount, setAmount] = useState('')
  const [provider, setProvider] = useState('')
  const [targetProviderID, setTargetProviderID] = useState('')
  const config = useQuery({ queryKey: ['public-config'], queryFn: () => publicApi<PublicConfig>('/config'), staleTime: 60_000 })
  const pricing = useQuery({ queryKey: ['public-pricing', settings.region, settings.currency], queryFn: () => publicApi<PublicPricing>('/pricing', { query: { region: settings.region, currency: settings.currency } }) })
  const providers = useQuery({ queryKey: ['payment-providers'], queryFn: () => api<unknown>('/payment-providers').then(asPage<Provider>) })
  const provisioning = useQuery({ queryKey: ['provider-provisioning-capabilities'], queryFn: () => api<unknown>('/provider-provisioning/capabilities').then(asPage<ProvisioningCapability>) })
  const automaticTargets = (provisioning.data?.items || []).filter((item) => item.enabled && item.supports_automatic_binding && item.supports_automatic_credit)
  const eligibleProviders = (providers.data?.items || []).filter((item) => paymentProviderRuntimeAdmitted(item, settings.region))
  const selectionConfirmed = Boolean(config.data?.supported_regions?.length && config.data?.supported_currencies?.length && config.data.supported_regions.includes(settings.region) && config.data.supported_currencies.includes(settings.currency))
  const organizationRegionReady = Boolean(scope.organization && scope.organization.billing_region === settings.region)
  const selectedFee = approvedPaymentChannelFee(pricing.data, provider)
  const anyApprovedChannelFee = approvedPaymentChannelFee(pricing.data)
  const commerceConfirmed = Boolean(organizationRegionReady && selectionConfirmed && commercialTermsApproved(pricing.data?.commercial_terms) && pricing.data?.payment_region_supported === true && selectedFee)
  const orders = useQuery({
    queryKey: ['recharge-orders', scope.organizationID],
    queryFn: () => api<unknown>(`/organizations/${scope.organizationID}/recharge-orders`).then(asPage<RechargeOrder>),
    enabled: Boolean(scope.organizationID), refetchInterval: 10_000,
  })
  useEffect(() => {
    if (config.data?.supported_regions?.length && !config.data.supported_regions.includes(settings.region)) settings.setRegion(config.data.supported_regions[0])
    if (config.data?.supported_currencies?.length && !config.data.supported_currencies.includes(settings.currency)) settings.setCurrency(config.data.supported_currencies[0])
  }, [config.data, settings])
  useEffect(() => {
    if (!eligibleProviders.some((item) => item.name === provider)) setProvider(eligibleProviders[0]?.name || '')
  }, [eligibleProviders, provider])
  const create = useMutation({
    mutationFn: () => api<CreateResponse>(`/organizations/${scope.organizationID}/recharge-orders`, {
      method: 'POST', body: JSON.stringify({ amount, currency: settings.currency, region: settings.region, payment_provider: provider, target_provider_id: targetProviderID || undefined, idempotency_key: crypto.randomUUID() }),
    }),
    onSuccess: (response) => {
      setAmount(''); void client.invalidateQueries({ queryKey: ['recharge-orders', scope.organizationID] })
      toast(`Recharge order ${response.order.platform_order_no} created`)
    },
  })
  return <div className="page-stack">
    <div className="page-header"><div><h1>Recharge</h1><p>Create a payment order and track server-verified wallet credit.</p></div></div>
    <Panel title="Create recharge order" description="The browser never credits the wallet. Only a verified server-side payment result can post a ledger journal.">
      <div className="panel-pad"><Form className="form-grid" onSubmit={() => create.mutateAsync()}>
        <label><span>Amount ({settings.currency})</span><input type="number" min="0.01" step="0.01" required value={amount} onChange={(event) => setAmount(event.target.value)} /></label>
        <label><span>Payment method</span><select required value={provider} disabled={!organizationRegionReady || !selectionConfirmed || !eligibleProviders.length} onChange={(event) => setProvider(event.target.value)}><option value="">Select an eligible payment method</option>{eligibleProviders.map((item) => <option key={item.name} value={item.name}>{item.name} · {item.contract_status}{item.production_ready ? '' : ' · non-production'}</option>)}</select></label>
        <label><span>Region</span><select required value={settings.region} disabled={!config.data?.supported_regions?.length} onChange={(event) => settings.setRegion(event.target.value)}>{(config.data?.supported_regions || [settings.region]).map((region) => <option value={region} key={region}>{region}</option>)}</select></label>
        <label><span>Currency</span><select required value={settings.currency} disabled={!config.data?.supported_currencies?.length} onChange={(event) => settings.setCurrency(event.target.value)}>{(config.data?.supported_currencies || [settings.currency]).map((currency) => <option value={currency} key={currency}>{currency}</option>)}</select></label>
        <label className="full-span"><span>Provider account after verified payment (optional)</span><select value={targetProviderID} disabled={provisioning.isLoading} onChange={(event) => setTargetProviderID(event.target.value)}><option value="">Wallet only — no upstream allocation</option>{automaticTargets.map((item) => <option key={item.provider_id} value={item.provider_id}>{item.provider_name} · {item.mode}</option>)}</select></label>
        <div className="inline-note full-span"><CreditCard size={14} />Only Providers with documented automatic binding and upstream credit-allocation APIs appear here. A wallet-only recharge never claims that funds were transferred to an upstream Provider.</div>
        {provisioning.isError && <div className="form-error full-span">Provider account capabilities are unavailable. Wallet-only recharge remains available.</div>}
        {config.isError && <div className="form-error full-span">Public region/currency configuration is unavailable. Recharge is disabled.</div>}
        {pricing.isError && <div className="form-error full-span">Published commercial terms or payment-fee evidence could not be loaded. Recharge is disabled.</div>}
        {pricing.isSuccess && (!commercialTermsApproved(pricing.data.commercial_terms) || !anyApprovedChannelFee || pricing.data.payment_region_supported !== true) && <div className="inline-warning full-span">Approved commercial terms or a runtime-admitted payment channel with approved fee evidence is unavailable for {settings.region} / {settings.currency}. Recharge is disabled.</div>}
        {providers.isError && <div className="form-error full-span">Payment provider availability could not be loaded. Recharge is disabled.</div>}
        {scope.organization && !organizationRegionReady && <div className="inline-warning full-span">Organization billing/customer region is {scope.organization.billing_region || 'unset'}, but the public quote is for {settings.region}. Confirm the organization region in onboarding before recharge.</div>}
        {config.isSuccess && (!config.data.supported_regions?.length || !config.data.supported_currencies?.length) && <div className="inline-warning full-span">No supported region/currency evidence is published. Recharge is disabled.</div>}
        {providers.isSuccess && selectionConfirmed && !eligibleProviders.length && <div className="inline-warning full-span">No enabled payment provider allows {settings.region}. Recharge is disabled.</div>}
        {pricing.isSuccess && provider && !selectedFee && <div className="inline-warning full-span">No current, lawyer-approved <code>PAYMENT_CHANNEL</code> fee disclosure exists for <strong>{provider}</strong>. A platform-service fee or another provider's fee is not evidence for this channel, so recharge is disabled.</div>}
        {selectedFee && <div className="inline-note full-span"><CreditCard size={14} />Selected-channel fee: {selectedFee.fee_kind === 'NONE' ? 'explicitly no channel fee' : `${formatMoney(selectedFee.fixed_amount, selectedFee.currency)} fixed + ${selectedFee.rate_bps} bps`} · charged to customer: {selectedFee.charged_to_customer ? 'yes' : 'no'}.</div>}
        <div className="full-span inline-note"><CreditCard size={14} /> Sandbox moves no real funds. Manual transfer requires administrator evidence review. Neither adapter represents an unsigned production payment channel.</div>
        {create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Order creation failed.'}</div>}
        <div className="full-span"><SubmitButton pending={create.isPending} disabled={!scope.organizationID || !provider || !commerceConfirmed || !eligibleProviders.length}>Create order</SubmitButton></div>
      </Form></div>
    </Panel>
    <section className="resource-panel">
      {orders.isLoading && <Skeleton rows={6} />}
      {orders.isError && <div className="panel-pad"><ErrorState error={orders.error} onRetry={() => orders.refetch()} /></div>}
      {orders.isSuccess && !orders.data.items.length && <EmptyState title="No recharge orders" description="Create an order above; a completed payment remains pending until the server verifies it." />}
      {!!orders.data?.items.length && <DataTable rows={orders.data.items} rowKey={(row) => row.id} columns={[
        { key: 'order', label: 'Order', render: (row) => <div className="primary-cell"><strong>{row.platform_order_no}</strong><small>{row.provider_order_no || 'Provider order pending'}</small></div> },
        { key: 'provider', label: 'Provider', render: (row) => <Badge tone="info">{row.payment_provider}</Badge> },
        { key: 'target', label: 'Upstream allocation', render: (row) => row.target_provider_id ? <Badge tone="violet">{row.target_provisioning_mode || 'queued'}</Badge> : <span className="muted-cell">Wallet only</span> },
        { key: 'amount', label: 'Amount', render: (row) => formatMoney(row.amount, row.currency) },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'created', label: 'Created', render: (row) => formatDate(row.created_at) },
      ]} />}
    </section>
  </div>
}

export function PaymentSuccessPage() {
  const scope = useProjectScope()
  const [params] = useSearchParams()
  const orderID = params.get('order_id') || ''
  const order = useQuery({
    queryKey: ['recharge-order', scope.organizationID, orderID],
    queryFn: () => api<RechargeOrder>(`/organizations/${scope.organizationID}/recharge-orders/${orderID}`),
    enabled: Boolean(scope.organizationID && orderID), refetchInterval: (query) => query.state.data?.status === 'CREDITED' ? false : 3_000,
  })
  return <div className="page-stack"><Panel title="Payment confirmation" description="This page only polls the server. It cannot and does not credit a wallet.">
    <div className="panel-pad">
      {!orderID && <ErrorState error={new Error('The order_id query parameter is required.')} />}
      {order.isLoading && <Skeleton rows={3} />}
      {order.isError && <ErrorState error={order.error} onRetry={() => order.refetch()} />}
      {order.data && <div className="page-stack"><div><StatusBadge value={order.data.status} /> <strong>{order.data.platform_order_no}</strong></div><p>{formatMoney(order.data.amount, order.data.currency)} · {order.data.payment_provider}</p><p>{order.data.status === 'CREDITED' ? 'The verified payment and wallet ledger entry are complete.' : 'Waiting for server-side payment verification and ledger posting.'}</p><Button size="sm" onClick={() => order.refetch()}><RefreshCw size={13} />Refresh status</Button></div>}
    </div>
  </Panel></div>
}
