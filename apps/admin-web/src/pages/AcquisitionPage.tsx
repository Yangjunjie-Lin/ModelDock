import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileCheck2, Megaphone, Plus, RefreshCw, Scale, WalletCards } from 'lucide-react'
import { api, asPage, formatDate, formatMoney, formatNumber } from '../lib/api'
import { Badge, Button, DataTable, EmptyState, ErrorState, Form, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

const funnelLabels: Record<string, string> = {
  HOMEPAGE_VISITED: 'Homepage visited',
  REGISTERED: 'Registered',
  EMAIL_VERIFIED: 'Email verified',
  API_KEY_CREATED: 'API key created',
  FIRST_RECHARGE: 'First recharge',
  FIRST_API_CALL: 'First API call',
  SECOND_API_CALL: 'Second API call',
  FIRST_SUBSCRIPTION: 'First subscription',
}

type FunnelSummary = {
  from: string
  to: string
  counts: Record<string, number>
  stages?: Array<{
    event_type: string
    count: number
    conversion_from_previous: number | null
  }>
  cohort_scope?: string[]
  call_success_semantics?: string
}

type CommercialTerms = {
  id: string
  region: string
  currency: string
  subscription_tax_included: boolean | null
  token_tax_included: boolean | null
  tax_disclosure: string
  refund_summary: string
  refund_policy_url: string
  bonus_credit_amount: string
  bonus_non_refundable: boolean
  legal_review_required: boolean
  legal_review_status: string
  effective_at: string
  expires_at?: string
  created_at: string
}

type PaymentFee = {
  id: string
  fee_category: string
  payment_provider: string
  region: string
  currency: string
  fee_kind: string
  fixed_amount: string
  rate_bps: number
  charged_to_customer: boolean
  description: string
  legal_review_required: boolean
  legal_review_status: string
  effective_at: string
  expires_at?: string
  created_at: string
}

type TriState = 'UNKNOWN' | 'INCLUDED' | 'EXCLUDED'

const today = new Date().toISOString().slice(0, 10)
const monthAgo = new Date(Date.now() - 30 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10)

function exactDecimal(value: string) {
  return /^(?:0|[1-9]\d*)(?:\.\d+)?$/.test(value)
}

function iso(value: string) {
  return value ? new Date(value).toISOString() : undefined
}

function taxValue(value: TriState) {
  return value === 'UNKNOWN' ? null : value === 'INCLUDED'
}

export function AcquisitionPage() {
  const [from, setFrom] = useState(monthAgo)
  const [to, setTo] = useState(today)
  const [termsOpen, setTermsOpen] = useState(false)
  const [feeOpen, setFeeOpen] = useState(false)
  const [termsKey, setTermsKey] = useState(() => crypto.randomUUID())
  const [feeKey, setFeeKey] = useState(() => crypto.randomUUID())
  const [termsForm, setTermsForm] = useState({ region: '', currency: '', subscription_tax_included: 'UNKNOWN' as TriState, token_tax_included: 'UNKNOWN' as TriState, tax_disclosure: '', refund_summary: '', refund_policy_url: '', bonus_credit_amount: '', bonus_non_refundable: true, effective_at: '', expires_at: '', legal_review_status: 'PENDING', legal_review_confirmed: false })
  const [feeForm, setFeeForm] = useState({ fee_category: 'PAYMENT_CHANNEL', payment_provider: '', region: '', currency: '', fee_kind: 'NONE', fixed_amount: '', rate_bps: '', charged_to_customer: false, description: '', effective_at: '', expires_at: '', legal_review_status: 'PENDING', legal_review_confirmed: false })
  const client = useQueryClient()
  const toast = useToast()

  const funnel = useQuery({
    queryKey: ['commercial-funnel', from, to],
    queryFn: () => api<FunnelSummary>('/funnel/summary', { query: { from: `${from}T00:00:00Z`, to: `${to}T23:59:59Z` } }),
    enabled: Boolean(from && to && from <= to),
  })
  const terms = useQuery({ queryKey: ['public-commercial-terms'], queryFn: () => api<unknown>('/public/commercial-terms', { query: { limit: 200 } }).then(asPage<CommercialTerms>) })
  const fees = useQuery({ queryKey: ['public-payment-fees'], queryFn: () => api<unknown>('/public/payment-fees', { query: { limit: 200 } }).then(asPage<PaymentFee>) })

  const publishTerms = useMutation({
    mutationFn: () => api('/public/commercial-terms', {
      method: 'POST',
      headers: { 'Idempotency-Key': termsKey },
      body: JSON.stringify({
        ...termsForm,
        region: termsForm.region.toUpperCase(),
        currency: termsForm.currency.toUpperCase(),
        subscription_tax_included: taxValue(termsForm.subscription_tax_included),
        token_tax_included: taxValue(termsForm.token_tax_included),
        effective_at: iso(termsForm.effective_at),
        expires_at: iso(termsForm.expires_at),
        idempotency_key: termsKey,
      }),
    }),
    onSuccess: () => {
      setTermsOpen(false)
      setTermsKey(crypto.randomUUID())
      void client.invalidateQueries({ queryKey: ['public-commercial-terms'] })
      toast('Immutable commercial disclosure version published')
    },
  })
  const publishFee = useMutation({
    mutationFn: () => api('/public/payment-fees', {
      method: 'POST',
      headers: { 'Idempotency-Key': feeKey },
      body: JSON.stringify({
        ...feeForm,
        region: feeForm.region.toUpperCase(),
        currency: feeForm.currency.toUpperCase(),
        payment_provider: feeForm.payment_provider.toLowerCase(),
        fixed_amount: feeForm.fixed_amount || '0',
        rate_bps: Number(feeForm.rate_bps || '0'),
        effective_at: iso(feeForm.effective_at),
        expires_at: iso(feeForm.expires_at),
        idempotency_key: feeKey,
      }),
    }),
    onSuccess: () => {
      setFeeOpen(false)
      setFeeKey(crypto.randomUUID())
      void client.invalidateQueries({ queryKey: ['public-payment-fees'] })
      toast('Immutable payment fee version published')
    },
  })

  const funnelRows = useMemo(() => {
    if (funnel.data?.stages?.length) {
      return funnel.data.stages.map((stage) => ({
        stage: stage.event_type,
        count: stage.count,
        conversion: stage.conversion_from_previous ?? undefined,
      }))
    }
    const stages = Object.keys(funnelLabels)
    return stages.map((stage, index) => {
      const count = funnel.data?.counts?.[stage] || 0
      const previous = index > 0 ? funnel.data?.counts?.[stages[index - 1]] || 0 : 0
      return { stage, count, conversion: index > 0 && previous > 0 ? count / previous : undefined }
    })
  }, [funnel.data])
  const maxCount = Math.max(...funnelRows.map((row) => row.count), 1)
  const termsValid = (termsForm.region === '*' || termsForm.region.length === 2) && termsForm.currency.length === 3 && termsForm.tax_disclosure.trim().length > 0 && termsForm.refund_summary.trim().length > 0 && termsForm.refund_policy_url.trim().length > 0 && exactDecimal(termsForm.bonus_credit_amount) && Boolean(termsForm.effective_at) && (termsForm.legal_review_status !== 'APPROVED' || termsForm.legal_review_confirmed)
  const feeNeedsFixed = ['FIXED', 'FIXED_PLUS_PERCENT'].includes(feeForm.fee_kind)
  const feeNeedsRate = ['PERCENT', 'FIXED_PLUS_PERCENT'].includes(feeForm.fee_kind)
  const feeValid = /^[a-z][a-z0-9_-]{1,31}$/.test(feeForm.payment_provider) && (feeForm.region === '*' || feeForm.region.length === 2) && feeForm.currency.length === 3 && (!feeNeedsFixed || exactDecimal(feeForm.fixed_amount) && feeForm.fixed_amount !== '0') && (!feeNeedsRate || Number.isInteger(Number(feeForm.rate_bps)) && Number(feeForm.rate_bps) > 0) && feeForm.description.trim().length > 0 && Boolean(feeForm.effective_at) && (feeForm.legal_review_status !== 'APPROVED' || feeForm.legal_review_confirmed)

  return <div className="page-stack acquisition-page">
    <div className="page-header"><div><div className="eyebrow-row"><Megaphone size={14} />ACQUISITION & PUBLIC COMMERCE</div><h1>Conversion and public disclosures</h1><p>Inspect aggregate acquisition stages and publish append-only, effective-dated commercial evidence. No user-level funnel identifiers are exposed.</p></div><div className="header-actions"><Button onClick={() => { void funnel.refetch(); void terms.refetch(); void fees.refetch() }}><RefreshCw size={14} />Refresh</Button><Button onClick={() => setTermsOpen(true)}><Scale size={14} />Publish terms</Button><Button variant="primary" onClick={() => setFeeOpen(true)}><Plus size={14} />Publish fee</Button></div></div>
    <Panel title="Commercial funnel" description="Counts are server-derived transactional milestones; homepage visits use HMAC-bounded anonymous events only."><div className="acquisition-range"><label><span>From (UTC)</span><input type="date" value={from} max={to} onChange={(event) => setFrom(event.target.value)} /></label><label><span>To (UTC)</span><input type="date" value={to} min={from} max={today} onChange={(event) => setTo(event.target.value)} /></label>{funnel.data && <small>{formatDate(funnel.data.from)} – {formatDate(funnel.data.to)} · cohort {funnel.data.cohort_scope?.join(', ') || 'server-defined'}</small>}</div>{funnel.isLoading && <Skeleton rows={8} />}{funnel.isError && <ErrorState error={funnel.error} onRetry={() => funnel.refetch()} />}{funnel.isSuccess && <div className="funnel-grid">{funnelRows.map((row, index) => <article key={row.stage}><header><span>{String(index + 1).padStart(2, '0')}</span><StatusBadge value={row.stage} /></header><strong>{formatNumber(row.count, false)}</strong><p>{funnelLabels[row.stage] || row.stage.replaceAll('_', ' ')}</p><div className="funnel-track"><i style={{ width: `${row.count / maxCount * 100}%` }} /></div><small>{index === 0 ? 'Entry cohort' : row.conversion === undefined ? 'Step conversion unavailable (previous stage is zero)' : `${(row.conversion * 100).toFixed(1)}% from previous stage`}</small></article>)}</div>}</Panel>
    <div className="acquisition-disclosure-grid"><Panel title="Commercial terms evidence" description="Null tax fields stay explicitly undisclosed; PENDING is not an approved public legal/tax representation." action={<Button size="sm" onClick={() => setTermsOpen(true)}><Plus size={13} />Publish</Button>}>{terms.isLoading && <Skeleton rows={5} />}{terms.isError && <ErrorState error={terms.error} onRetry={() => terms.refetch()} />}{terms.isSuccess && !terms.data.items.length && <EmptyState title="No commercial terms published" description="Public checkout remains fail-closed until an effective approved disclosure exists." />}{terms.data?.items.length ? <DataTable rows={terms.data.items} rowKey={(row) => row.id} columns={[{ key: 'scope', label: 'Scope', render: (row) => <div className="primary-cell"><strong>{row.region} / {row.currency}</strong><small>{formatDate(row.effective_at)}{row.expires_at ? ` – ${formatDate(row.expires_at)}` : ''}</small></div> }, { key: 'tax', label: 'Tax', render: (row) => <div className="primary-cell"><span>Subscription: {taxDisclosure(row.subscription_tax_included)}</span><small>Token: {taxDisclosure(row.token_tax_included)}</small></div> }, { key: 'bonus', label: 'Bonus', render: (row) => <div className="primary-cell"><strong>{formatMoney(row.bonus_credit_amount, row.currency)}</strong><small>{row.bonus_non_refundable ? 'Non-refundable' : 'Refund treatment disclosed separately'}</small></div> }, { key: 'review', label: 'Legal review', render: (row) => <StatusBadge value={row.legal_review_status} /> }]} /> : null}</Panel><Panel title="Payment and platform fee evidence" description="An empty list does not mean zero fees; the public page remains explicit and fail-closed." action={<Button size="sm" onClick={() => setFeeOpen(true)}><Plus size={13} />Publish</Button>}>{fees.isLoading && <Skeleton rows={5} />}{fees.isError && <ErrorState error={fees.error} onRetry={() => fees.refetch()} />}{fees.isSuccess && !fees.data.items.length && <EmptyState title="No payment fee evidence" description="No zero-fee claim will be inferred." />}{fees.data?.items.length ? <DataTable rows={fees.data.items} rowKey={(row) => row.id} columns={[{ key: 'provider', label: 'Provider', render: (row) => <div className="primary-cell"><strong>{row.payment_provider}</strong><small>{row.fee_category} · {row.region}</small></div> }, { key: 'fee', label: 'Fee', render: (row) => <div className="primary-cell"><strong>{formatMoney(row.fixed_amount, row.currency)}</strong><small>{row.rate_bps} bps · {row.fee_kind}</small></div> }, { key: 'customer', label: 'Charged to customer', render: (row) => <Badge tone={row.charged_to_customer ? 'warning' : 'neutral'}>{row.charged_to_customer ? 'Yes' : 'No'}</Badge> }, { key: 'review', label: 'Legal review', render: (row) => <StatusBadge value={row.legal_review_status} /> }]} /> : null}</Panel></div>

    <Modal open={termsOpen} onClose={() => setTermsOpen(false)} title="Publish commercial terms evidence" description="This creates an immutable version. Publishing APPROVED requires an explicit legal-review confirmation." wide footer={<><Button onClick={() => setTermsOpen(false)}>Cancel</Button><SubmitButton form="publish-terms" pending={publishTerms.isPending} disabled={!termsValid}>Publish immutable version</SubmitButton></>}><Form id="publish-terms" className="form-grid" onSubmit={() => publishTerms.mutateAsync()}><label><span>Region *</span><input required pattern="[A-Z*]{1,2}" value={termsForm.region} onChange={(event) => setTermsForm({ ...termsForm, region: event.target.value.toUpperCase() })} /></label><label><span>Currency *</span><input required pattern="[A-Z]{3}" maxLength={3} value={termsForm.currency} onChange={(event) => setTermsForm({ ...termsForm, currency: event.target.value.toUpperCase() })} /></label><label><span>Subscription tax *</span><select value={termsForm.subscription_tax_included} onChange={(event) => setTermsForm({ ...termsForm, subscription_tax_included: event.target.value as TriState })}><option value="UNKNOWN">Undisclosed / pending</option><option value="INCLUDED">Included</option><option value="EXCLUDED">Not included</option></select></label><label><span>Token tax *</span><select value={termsForm.token_tax_included} onChange={(event) => setTermsForm({ ...termsForm, token_tax_included: event.target.value as TriState })}><option value="UNKNOWN">Undisclosed / pending</option><option value="INCLUDED">Included</option><option value="EXCLUDED">Not included</option></select></label><label className="full-span"><span>Tax disclosure *</span><textarea required maxLength={4000} value={termsForm.tax_disclosure} onChange={(event) => setTermsForm({ ...termsForm, tax_disclosure: event.target.value })} /></label><label className="full-span"><span>Refund summary *</span><textarea required maxLength={4000} value={termsForm.refund_summary} onChange={(event) => setTermsForm({ ...termsForm, refund_summary: event.target.value })} /></label><label><span>Refund policy URL *</span><input required value={termsForm.refund_policy_url} onChange={(event) => setTermsForm({ ...termsForm, refund_policy_url: event.target.value })} placeholder="/legal/refunds" /></label><label><span>Bonus credit amount *</span><input required inputMode="decimal" pattern="(?:0|[1-9][0-9]*)(?:\.[0-9]+)?" value={termsForm.bonus_credit_amount} onChange={(event) => setTermsForm({ ...termsForm, bonus_credit_amount: event.target.value })} /></label><label><span>Effective at *</span><input required type="datetime-local" value={termsForm.effective_at} onChange={(event) => setTermsForm({ ...termsForm, effective_at: event.target.value })} /></label><label><span>Expires at</span><input type="datetime-local" min={termsForm.effective_at} value={termsForm.expires_at} onChange={(event) => setTermsForm({ ...termsForm, expires_at: event.target.value })} /></label><label><span>Legal review status</span><select value={termsForm.legal_review_status} onChange={(event) => setTermsForm({ ...termsForm, legal_review_status: event.target.value, legal_review_confirmed: false })}><option value="PENDING">Pending counsel review</option><option value="APPROVED">Approved</option></select></label><label className="finance-confirm"><input type="checkbox" checked={termsForm.bonus_non_refundable} onChange={(event) => setTermsForm({ ...termsForm, bonus_non_refundable: event.target.checked })} /><span>Bonus is non-refundable</span></label>{termsForm.legal_review_status === 'APPROVED' && <label className="finance-confirm full-span"><input type="checkbox" checked={termsForm.legal_review_confirmed} onChange={(event) => setTermsForm({ ...termsForm, legal_review_confirmed: event.target.checked })} /><span>I confirm qualified counsel reviewed and approved this exact disclosure version.</span></label>}<div className="inline-warning full-span"><FileCheck2 size={14} />Idempotency key <code>{termsKey}</code> is retained for retries until this form succeeds.</div>{publishTerms.isError && <div className="form-error full-span">{publishTerms.error instanceof Error ? publishTerms.error.message : 'Publish failed'}</div>}</Form></Modal>

    <Modal open={feeOpen} onClose={() => setFeeOpen(false)} title="Publish payment or platform fee evidence" description="This creates an immutable effective-dated version; exact amounts remain decimal strings." wide footer={<><Button onClick={() => setFeeOpen(false)}>Cancel</Button><SubmitButton form="publish-fee" pending={publishFee.isPending} disabled={!feeValid}>Publish immutable version</SubmitButton></>}><Form id="publish-fee" className="form-grid" onSubmit={() => publishFee.mutateAsync()}><label><span>Fee category *</span><select value={feeForm.fee_category} onChange={(event) => setFeeForm({ ...feeForm, fee_category: event.target.value })}><option value="PAYMENT_CHANNEL">Payment channel</option><option value="PLATFORM_SERVICE">Platform service</option></select></label><label><span>Payment provider *</span><input required pattern="[a-z][a-z0-9_-]{1,31}" value={feeForm.payment_provider} onChange={(event) => setFeeForm({ ...feeForm, payment_provider: event.target.value.toLowerCase() })} /></label><label><span>Region *</span><input required pattern="[A-Z*]{1,2}" value={feeForm.region} onChange={(event) => setFeeForm({ ...feeForm, region: event.target.value.toUpperCase() })} /></label><label><span>Currency *</span><input required pattern="[A-Z]{3}" maxLength={3} value={feeForm.currency} onChange={(event) => setFeeForm({ ...feeForm, currency: event.target.value.toUpperCase() })} /></label><label><span>Fee kind *</span><select value={feeForm.fee_kind} onChange={(event) => setFeeForm({ ...feeForm, fee_kind: event.target.value })}><option value="NONE">Explicitly none</option><option value="FIXED">Fixed</option><option value="PERCENT">Percentage</option><option value="FIXED_PLUS_PERCENT">Fixed + percentage</option></select></label><label><span>Fixed amount</span><input required={feeNeedsFixed} disabled={!feeNeedsFixed} inputMode="decimal" pattern="(?:0|[1-9][0-9]*)(?:\.[0-9]+)?" value={feeForm.fixed_amount} onChange={(event) => setFeeForm({ ...feeForm, fixed_amount: event.target.value })} /></label><label><span>Rate (basis points)</span><input required={feeNeedsRate} disabled={!feeNeedsRate} type="number" min="0" max="100000" step="1" value={feeForm.rate_bps} onChange={(event) => setFeeForm({ ...feeForm, rate_bps: event.target.value })} /></label><label className="finance-confirm"><input type="checkbox" checked={feeForm.charged_to_customer} onChange={(event) => setFeeForm({ ...feeForm, charged_to_customer: event.target.checked })} /><span>Charged directly to customer</span></label><label className="full-span"><span>Public description *</span><textarea required maxLength={4000} value={feeForm.description} onChange={(event) => setFeeForm({ ...feeForm, description: event.target.value })} /></label><label><span>Effective at *</span><input required type="datetime-local" value={feeForm.effective_at} onChange={(event) => setFeeForm({ ...feeForm, effective_at: event.target.value })} /></label><label><span>Expires at</span><input type="datetime-local" min={feeForm.effective_at} value={feeForm.expires_at} onChange={(event) => setFeeForm({ ...feeForm, expires_at: event.target.value })} /></label><label><span>Legal review status</span><select value={feeForm.legal_review_status} onChange={(event) => setFeeForm({ ...feeForm, legal_review_status: event.target.value, legal_review_confirmed: false })}><option value="PENDING">Pending counsel review</option><option value="APPROVED">Approved</option></select></label>{feeForm.legal_review_status === 'APPROVED' && <label className="finance-confirm full-span"><input type="checkbox" checked={feeForm.legal_review_confirmed} onChange={(event) => setFeeForm({ ...feeForm, legal_review_confirmed: event.target.checked })} /><span>I confirm qualified counsel reviewed and approved this exact fee disclosure.</span></label>}<div className="inline-warning full-span"><WalletCards size={14} />Idempotency key <code>{feeKey}</code> is retained for retries until this form succeeds.</div>{publishFee.isError && <div className="form-error full-span">{publishFee.error instanceof Error ? publishFee.error.message : 'Publish failed'}</div>}</Form></Modal>
  </div>
}

function taxDisclosure(value: boolean | null) {
  return value === true ? 'Included' : value === false ? 'Not included' : 'Undisclosed / pending'
}
