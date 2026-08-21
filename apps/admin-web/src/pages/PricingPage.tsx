import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Calculator, Plus, ShieldCheck } from 'lucide-react'
import { api, asPage } from '../lib/api'
import type { Row } from '../lib/types'
import { Badge, Button, DataTable, EmptyState, ErrorState, Form, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

const zeroRates = { input_token_price: '', cached_input_token_price: '', output_token_price: '', request_fixed_price: '' }

export function PricingPage() {
  const costs = useQuery({ queryKey: ['pricing-cost-books'], queryFn: () => api<unknown>('/pricing/provider-cost-price-books').then(asPage<Row>) })
  const retail = useQuery({ queryKey: ['pricing-retail-books'], queryFn: () => api<unknown>('/pricing/customer-retail-price-books').then(asPage<Row>) })
  const plans = useQuery({ queryKey: ['pricing-plans'], queryFn: () => api<unknown>('/pricing/organization-price-plans').then(asPage<Row>) })
  const credits = useQuery({ queryKey: ['pricing-promotion-credits'], queryFn: () => api<unknown>('/pricing/promotion-credits').then(asPage<Row>) })
  const changes = useQuery({ queryKey: ['pricing-cost-changes'], queryFn: () => api<unknown>('/pricing/provider-cost-changes').then(asPage<Row>) })
  const byokFees = useQuery({ queryKey: ['pricing-byok-fees'], queryFn: () => api<unknown>('/pricing/byok-service-fee-policies').then(asPage<Row>) })
  const failed = costs.error || retail.error || plans.error || credits.error || changes.error || byokFees.error
  const loading = costs.isLoading || retail.isLoading || plans.isLoading || credits.isLoading || changes.isLoading || byokFees.isLoading
  return <div className="page-stack">
    <div className="page-header"><div><h1>Commercial pricing</h1><p>Publish cost and retail books, inspect plan priority, quote exact charges, and keep non-refundable promotions separate from cash.</p></div><div className="row-actions"><BYOKFeeCreateButton /><PriceCreateButton /></div></div>
    <QuotePanel />
    {loading && <Panel><Skeleton rows={8} /></Panel>}
    {failed && <ErrorState error={failed} onRetry={() => { void costs.refetch(); void retail.refetch(); void plans.refetch(); void credits.refetch() }} />}
    {!loading && !failed && <>
      <PriceTable title="Provider cost price book" rows={costs.data?.items || []} kind="cost" />
      <CostChangeTable rows={changes.data?.items || []} />
      <BYOKFeeTable rows={byokFees.data?.items || []} />
      <PriceTable title="Customer retail price book" rows={retail.data?.items || []} kind="retail" />
      <PriceTable title="Organization price plans" rows={plans.data?.items || []} kind="retail" />
      <PriceTable title="Promotion credits (non-refundable)" rows={credits.data?.items || []} kind="credit" />
    </>}
  </div>
}

function BYOKFeeTable({ rows }: { rows: Row[] }) {
  const client = useQueryClient(); const toast = useToast()
  const disable = useMutation({ mutationFn: (id: string) => api(`/pricing/byok-service-fee-policies/${id}`, { method: 'DELETE' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['pricing-byok-fees'] }); toast('BYOK service fee disabled') } })
  if (!rows.length) return <Panel title="BYOK platform service fees"><EmptyState title="No BYOK fee policies" description="BYOK traffic fails closed until an applicable policy is effective." /></Panel>
  return <Panel title="BYOK platform service fees" description="Policies are append-only. Publish a new version for rate changes; disabling is permanent."><DataTable rows={rows} rowKey={(row) => String(row.id)} columns={[{ key: 'scope', label: 'Scope', render: (row) => <div className="primary-cell"><code>{String(row.organization_id || 'All organizations')}</code><small>{String(row.provider_id || 'All Providers')}</small></div> }, { key: 'fees', label: 'Fixed / input / output', render: (row) => <code>{`${row.fixed_fee} / ${row.input_token_fee} / ${row.output_token_fee}`}</code> }, { key: 'currency', label: 'Currency / unit', render: (row) => `${row.currency} / ${row.unit}` }, { key: 'effective', label: 'Effective', render: (row) => String(row.effective_at) }, { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.enabled ? 'ACTIVE' : 'DISABLED'} /> }, { key: 'actions', label: '', render: (row) => <Button size="sm" variant="danger" disabled={!row.enabled || disable.isPending} onClick={() => disable.mutate(String(row.id))}>Disable</Button> }]}/></Panel>
}

function BYOKFeeCreateButton() {
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<Row>({ organization_id: '', provider_id: '', fixed_fee: '0', input_token_fee: '0', cached_input_token_fee: '0', output_token_fee: '0', currency: 'USD', unit: '1000000', effective_at: new Date().toISOString() })
  const client = useQueryClient(); const toast = useToast()
  const set = (key: string, value: string) => setForm((current) => ({ ...current, [key]: value }))
  const save = useMutation({ mutationFn: () => api('/pricing/byok-service-fee-policies', { method: 'POST', body: JSON.stringify({ ...form, organization_id: form.organization_id || undefined, provider_id: form.provider_id || undefined, unit: Number(form.unit) }) }), onSuccess: () => { setOpen(false); void client.invalidateQueries({ queryKey: ['pricing-byok-fees'] }); toast('BYOK service fee published') } })
  return <><Button onClick={() => setOpen(true)}><ShieldCheck size={14} />BYOK fee</Button><Modal open={open} onClose={() => setOpen(false)} title="Publish BYOK service fee" description="The policy is immutable after publication. Empty scope fields create a global fallback." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="byok-fee" pending={save.isPending}>Publish</SubmitButton></>}><Form id="byok-fee" className="form-grid" onSubmit={() => save.mutateAsync()}><label><span>Organization ID</span><input value={String(form.organization_id)} onChange={(event) => set('organization_id', event.target.value)} /></label><label><span>Provider ID</span><input value={String(form.provider_id)} onChange={(event) => set('provider_id', event.target.value)} /></label>{(['fixed_fee','input_token_fee','cached_input_token_fee','output_token_fee'] as const).map((key) => <label key={key}><span>{key.replaceAll('_',' ')} *</span><input required inputMode="decimal" value={String(form[key])} onChange={(event) => set(key,event.target.value)} /></label>)}<label><span>Currency *</span><input required maxLength={3} value={String(form.currency)} onChange={(event) => set('currency',event.target.value.toUpperCase())} /></label><label><span>Unit *</span><input required type="number" min="1" value={String(form.unit)} onChange={(event) => set('unit',event.target.value)} /></label><label className="full-span"><span>Effective at *</span><input required value={String(form.effective_at)} onChange={(event) => set('effective_at',event.target.value)} /></label>{save.isError&&<div className="form-error full-span">{save.error instanceof Error?save.error.message:'Unable to publish policy.'}</div>}</Form></Modal></>
}

function CostChangeTable({ rows }: { rows: Row[] }) {
  const client = useQueryClient(); const toast = useToast()
  const review = useMutation({ mutationFn: ({ id, decision }: { id: string; decision: string }) => api(`/pricing/provider-cost-changes/${id}/review`, { method: 'POST', body: JSON.stringify({ decision, reason: 'Reviewed in ModelDock admin pricing console' }) }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['pricing-cost-changes'] }); void client.invalidateQueries({ queryKey: ['pricing-cost-books'] }); toast('Cost change reviewed') } })
  if (!rows.length) return <Panel title="Provider cost change approval"><EmptyState title="No cost changes pending" /></Panel>
  return <Panel title="Provider cost change approval" description="Manual, API, and CSV inputs share one approval workflow. Approval appends a future-effective price and emits an alert."><DataTable rows={rows} rowKey={(row) => String(row.id)} columns={[{ key: 'source', label: 'Source', render: (row) => <Badge>{String(row.source_type)}</Badge> }, { key: 'provider', label: 'Provider / model', render: (row) => <div className="primary-cell"><code>{String(row.provider_id)}</code><small>{String(row.model_id)}</small></div> }, { key: 'effective', label: 'Effective', render: (row) => String(row.effective_at) }, { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> }, { key: 'actions', label: '', render: (row) => <div className="row-actions"><Button size="sm" variant="primary" disabled={row.status !== 'PENDING' || review.isPending} onClick={() => review.mutate({ id: String(row.id), decision: 'APPROVE' })}>Approve</Button><Button size="sm" variant="danger" disabled={row.status !== 'PENDING' || review.isPending} onClick={() => review.mutate({ id: String(row.id), decision: 'REJECT' })}>Reject</Button></div> }]}/></Panel>
}

function PriceTable({ title, rows, kind }: { title: string; rows: Row[]; kind: 'cost' | 'retail' | 'credit' }) {
  if (!rows.length) return <Panel title={title}><EmptyState title="No published records" /></Panel>
  const price = (row: Row, component: string) => row[`${component}_${kind === 'cost' ? 'cost' : 'price'}`]
  return <Panel title={title}><DataTable rows={rows} rowKey={(row) => String(row.id)} columns={kind === 'credit' ? [
    { key: 'organization_id', label: 'Organization', render: (row) => <code>{String(row.organization_id)}</code> },
    { key: 'amount_remaining', label: 'Remaining', render: (row) => <strong>{`${row.currency || 'USD'} ${row.amount_remaining}`}</strong> },
    { key: 'source', label: 'Source', render: (row) => String(row.source) },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'refundable', label: 'Refundable cash', render: () => <Badge tone="warning">No</Badge> },
  ] : [
    { key: 'provider_id', label: 'Provider', render: (row) => <code>{String(row.provider_id)}</code> },
    { key: 'model_id', label: 'Model', render: (row) => <code>{String(row.model_id)}</code> },
    { key: 'input', label: 'Input / unit', render: (row) => String(price(row, 'input_token')) },
    { key: 'cached', label: 'Cached / unit', render: (row) => String(price(row, 'cached_input_token')) },
    { key: 'output', label: 'Output / unit', render: (row) => String(price(row, 'output_token')) },
    { key: 'fixed', label: 'Fixed request', render: (row) => String(price(row, 'request_fixed')) },
    { key: 'approval_status', label: 'Approval', render: (row) => <StatusBadge value={row.approval_status} /> },
  ]} /></Panel>
}

function QuotePanel() {
  const [organizationID, setOrganizationID] = useState('')
  const [providerID, setProviderID] = useState('')
  const [model, setModel] = useState('')
  const [input, setInput] = useState('1000')
  const [output, setOutput] = useState('500')
  const quote = useMutation({ mutationFn: () => api<Row>('/pricing/quotes', { method: 'POST', body: JSON.stringify({ organization_id: organizationID, provider_id: providerID || undefined, model, estimated_input_tokens: Number(input), estimated_output_tokens: Number(output), idempotency_key: crypto.randomUUID() }) }) })
  const result = (quote.data?.quote || quote.data?.data || quote.data) as Row | undefined
  return <Panel title="Pricing quote" description="Resolves override > subscription > customer > system default in one repeatable-read transaction.">
    <Form className="form-grid" onSubmit={() => quote.mutateAsync()}><label><span>Organization ID *</span><input required value={organizationID} onChange={(event) => setOrganizationID(event.target.value)} /></label><label><span>Provider ID</span><input value={providerID} onChange={(event) => setProviderID(event.target.value)} /></label><label><span>Model *</span><input required value={model} onChange={(event) => setModel(event.target.value)} /></label><label><span>Estimated input tokens</span><input type="number" min="0" required value={input} onChange={(event) => setInput(event.target.value)} /></label><label><span>Estimated output tokens</span><input type="number" min="0" required value={output} onChange={(event) => setOutput(event.target.value)} /></label><div><SubmitButton pending={quote.isPending}><Calculator size={14} />Quote</SubmitButton></div></Form>
    {quote.isError && <div className="form-error">{quote.error instanceof Error ? quote.error.message : 'Quote failed.'}</div>}
    {result && <div className="metric-grid"><Panel title="Provider cost"><strong>{`${result.currency || 'USD'} ${result.provider_cost_amount}`}</strong></Panel><Panel title="Retail"><strong>{`${result.currency || 'USD'} ${result.retail_amount}`}</strong></Panel><Panel title="Promotion"><strong>{`${result.currency || 'USD'} ${result.promotion_amount}`}</strong></Panel><Panel title="Final due"><strong>{`${result.currency || 'USD'} ${result.final_amount}`}</strong><small>{`Version ${result.pricing_version_id}`}</small></Panel></div>}
  </Panel>
}

function PriceCreateButton() {
  const [open, setOpen] = useState(false)
  const [kind, setKind] = useState<'cost' | 'retail'>('retail')
  const [form, setForm] = useState<Row>({ provider_id: '', model_id: '', currency: 'USD', unit: '1000000', source: 'manual', ...zeroRates })
  const [force, setForce] = useState(false)
  const [confirmation, setConfirmation] = useState('')
  const [costSource, setCostSource] = useState<'MANUAL' | 'API' | 'CSV'>('MANUAL')
  const [sourceURL, setSourceURL] = useState('')
  const [csvFile, setCSVFile] = useState<File | null>(null)
  const [effectiveAt, setEffectiveAt] = useState(() => new Date().toISOString())
  const client = useQueryClient(); const toast = useToast()
  const save = useMutation({ mutationFn: async () => {
    if (kind === 'cost' && costSource === 'API') return api('/pricing/provider-cost-changes/fetch', { method: 'POST', headers: { 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ provider_id: form.provider_id, source_url: sourceURL }) })
    if (kind === 'cost' && costSource === 'CSV') {
      if (!csvFile) throw new Error('Select a CSV file.')
      return api('/pricing/provider-cost-changes/import-csv', { method: 'POST', query: { provider_id: String(form.provider_id) }, headers: { 'Content-Type': 'text/csv', 'Idempotency-Key': crypto.randomUUID() }, body: await csvFile.text() })
    }
    const body = kind === 'cost' ? { provider_id: form.provider_id, model_id: form.model_id, input_token_cost: form.input_token_price, cached_input_token_cost: form.cached_input_token_price, output_token_cost: form.output_token_price, request_fixed_cost: form.request_fixed_price, currency: form.currency, unit: Number(form.unit), source_reference: String(form.source || ''), effective_at: effectiveAt, idempotency_key: crypto.randomUUID() } : { ...form, unit: Number(form.unit), force_override: force, confirmation }
    return api(`/pricing/${kind === 'cost' ? 'provider-cost-changes/manual' : 'customer-retail-price-books'}`, { method: 'POST', body: JSON.stringify(body) })
  }, onSuccess: () => { setOpen(false); void client.invalidateQueries({ queryKey: [kind === 'cost' ? 'pricing-cost-books' : 'pricing-retail-books'] }); if (kind === 'cost') void client.invalidateQueries({ queryKey: ['pricing-cost-changes'] }); toast(kind === 'cost' ? 'Cost change submitted for approval' : 'Price version published') } })
  const set = (key: string, value: string) => setForm((current) => ({ ...current, [key]: value }))
  const ratesRequired = kind !== 'cost' || costSource === 'MANUAL'
  return <><Button onClick={() => setOpen(true)}><Plus size={14} />Publish price</Button><Modal open={open} onClose={() => setOpen(false)} title="Publish immutable price" description="Provider costs require approval before an append-only version becomes effective." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="publish-price" pending={save.isPending}>{kind === 'cost' ? 'Submit for approval' : 'Publish'}</SubmitButton></>}><Form id="publish-price" className="form-grid" onSubmit={() => save.mutateAsync()}><label><span>Price kind *</span><select value={kind} onChange={(event) => setKind(event.target.value as 'cost' | 'retail')}><option value="retail">Customer retail</option><option value="cost">Provider cost</option></select></label>{kind === 'cost' && <><label><span>Source *</span><select value={costSource} onChange={(event) => setCostSource(event.target.value as typeof costSource)}><option>MANUAL</option><option>API</option><option>CSV</option></select></label>{costSource === 'MANUAL' && <label><span>Effective at *</span><input required value={effectiveAt} onChange={(event) => setEffectiveAt(event.target.value)} /></label>}{costSource === 'API' && <label className="full-span"><span>Official HTTPS pricing URL *</span><input required type="url" value={sourceURL} onChange={(event) => setSourceURL(event.target.value)} /></label>}{costSource === 'CSV' && <label className="full-span"><span>CSV file (1 MiB / 500 rows max) *</span><input required type="file" accept="text/csv,.csv" onChange={(event) => setCSVFile(event.target.files?.[0] || null)} /></label>}</>}<label><span>Provider ID *</span><input required value={String(form.provider_id)} onChange={(event) => set('provider_id', event.target.value)} /></label>{ratesRequired && <><label><span>Model ID *</span><input required value={String(form.model_id)} onChange={(event) => set('model_id', event.target.value)} /></label>{(['input_token_price','cached_input_token_price','output_token_price','request_fixed_price'] as const).map((key) => <label key={key}><span>{key.replaceAll('_',' ')} *</span><input required inputMode="decimal" value={String(form[key])} onChange={(event) => set(key,event.target.value)} /></label>)}<label><span>Currency *</span><input required maxLength={3} value={String(form.currency)} onChange={(event) => set('currency',event.target.value.toUpperCase())} /></label><label><span>Unit *</span><input type="number" min="1" required value={String(form.unit)} onChange={(event) => set('unit',event.target.value)} /></label></>}{kind === 'retail' && <><label className="full-span"><input type="checkbox" checked={force} onChange={(event) => setForce(event.target.checked)} /> Force below-margin publication</label>{force && <label className="full-span"><span>Second confirmation *</span><input required value={confirmation} onChange={(event) => setConfirmation(event.target.value)} placeholder="CONFIRM_NEGATIVE_MARGIN_OVERRIDE" /></label>}</>}{save.isError && <div className="form-error full-span">{save.error instanceof Error ? save.error.message : 'Publish failed.'}</div>}</Form></Modal></>
}
