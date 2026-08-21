import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertOctagon, CheckCircle2, Play, RefreshCw, Rocket, ShieldCheck } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import { Button, DataTable, EmptyState, ErrorState, Form, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type Listing = { id: string; provider_id: string; provider_name: string; endpoint: string; supported_models: string[]; status: string; verified: boolean }
type Gate = { id: string; gate_code: string; evidence_source: string; status: string; evidence_reference: string; evaluated_at?: string }
type Review = { id: string; listing_id: string; provider_id: string; provider_name: string; supplier_id: string; supplier_name: string; revision: number; policy_version: string; status: string; created_by: string; approved_at?: string; created_at: string; passed_gate_count: number; gate_count: number; gates: Gate[] }
type Readiness = { supplier_id: string; supplier_name: string; contract_status: string; contract_evidence_reference: string; tax_status: string; tax_evidence_reference: string; payment_status: string; payment_evidence_reference: string; security_status: string; security_evidence_reference: string; production_payout_enabled: boolean; review_reason: string; version: number }

const readinessFields = [
  ['contract', 'Contract review'], ['tax', 'Tax review'], ['payment', 'Payment review'], ['security', 'Security review'],
] as const

const emptyReadiness: Readiness = { supplier_id: '', supplier_name: '', contract_status: 'PENDING', contract_evidence_reference: '', tax_status: 'PENDING', tax_evidence_reference: '', payment_status: 'PENDING', payment_evidence_reference: '', security_status: 'PENDING', security_evidence_reference: '', production_payout_enabled: false, review_reason: '', version: 0 }

export function MarketplaceLaunchPage() {
  const client = useQueryClient()
  const toast = useToast()
  const listings = useQuery({ queryKey: ['marketplace-listings'], queryFn: () => api<unknown>('/marketplace/providers').then(asPage<Listing>) })
  const rows = useMemo(() => listings.data?.items || [], [listings.data?.items])
  const [selectedID, setSelectedID] = useState('')
  const selected = rows.find((item) => item.id === selectedID) || rows[0]
  const reviews = useQuery({ queryKey: ['marketplace-launch-reviews', selected?.id], enabled: Boolean(selected?.id), queryFn: () => api<unknown>('/marketplace/launch-reviews', { query: { listing_id: selected.id, limit: 20 } }).then(asPage<Review>) })
  const review = reviews.data?.items[0]
  const readinessQuery = useQuery({ queryKey: ['marketplace-payout-readiness', review?.supplier_id], enabled: Boolean(review?.supplier_id), queryFn: () => api<Readiness>(`/marketplace/payout-readiness/${review?.supplier_id}`) })
  const [readiness, setReadiness] = useState<Readiness>(emptyReadiness)
  const [reason, setReason] = useState('')
  const [attestations, setAttestations] = useState<Record<string, string>>({})

  useEffect(() => { if (selected) setSelectedID(selected.id) }, [selected])
  useEffect(() => { if (readinessQuery.data) setReadiness(readinessQuery.data) }, [readinessQuery.data])

  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['marketplace-listings'] })
    void client.invalidateQueries({ queryKey: ['marketplace-launch-reviews'] })
    void client.invalidateQueries({ queryKey: ['marketplace-payout-readiness'] })
  }
  const createReview = useMutation({ mutationFn: () => api(`/marketplace/providers/${selected.id}/launch-reviews`, { method: 'POST', headers: { 'Idempotency-Key': `marketplace-launch-${crypto.randomUUID()}` }, body: JSON.stringify({ policy_version: 'marketplace-launch-2026-08-21' }) }), onSuccess: () => { toast('Launch review created'); refresh() } })
  const evaluate = useMutation({ mutationFn: () => api(`/marketplace/launch-reviews/${review?.id}/evaluate`, { method: 'POST' }), onSuccess: () => { toast('Platform evidence evaluated'); refresh() } })
  const approve = useMutation({ mutationFn: () => api(`/marketplace/launch-reviews/${review?.id}/approve`, { method: 'POST', body: JSON.stringify({ reason }) }), onSuccess: () => { toast('Marketplace release approved'); setReason(''); refresh() } })
  const lifecycle = useMutation({ mutationFn: (action: string) => api(`/marketplace/providers/${selected.id}/lifecycle`, { method: 'POST', body: JSON.stringify({ action, reason }) }), onSuccess: () => { toast('Lifecycle action recorded'); setReason(''); refresh() } })
  const attest = useMutation({ mutationFn: (gate: Gate) => api(`/marketplace/launch-reviews/${review?.id}/gates/${gate.gate_code}`, { method: 'PUT', body: JSON.stringify({ status: 'PASSED', evidence_reference: attestations[gate.gate_code], reason }) }), onSuccess: () => { toast('Drill evidence attested'); refresh() } })
  const saveReadiness = useMutation({ mutationFn: () => api(`/marketplace/payout-readiness/${review?.supplier_id}`, { method: 'PUT', body: JSON.stringify({ ...readiness, expected_version: readiness.version, review_reason: reason }) }), onSuccess: () => { toast('Payout readiness reviewed'); setReason(''); refresh() } })

  if (listings.isLoading) return <div className="page-stack"><Panel><Skeleton rows={8} /></Panel></div>
  if (listings.isError) return <ErrorState error={listings.error} onRetry={() => listings.refetch()} />
  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><Rocket size={14} />MARKETPLACE RELEASE CONTROL</div><h1>Provider Marketplace launch</h1><p>Platform evidence—not supplier declarations—controls canary traffic, ranking, activation, settlement readiness, suspension, cutover, and exit.</p></div><Button onClick={refresh}><RefreshCw size={14} />Refresh</Button></div>
    <div className="metric-grid compact-metrics">
      <Panel title="Marketplace listings"><strong className="billing-total">{rows.length}</strong></Panel>
      <Panel title="Production approved"><strong className="billing-total">{rows.filter((item) => item.status === 'ACTIVE').length}</strong></Panel>
      <Panel title="Blocked / suspended"><strong className="billing-total">{rows.filter((item) => ['SUSPENDED', 'REJECTED', 'EXITED'].includes(item.status)).length}</strong></Panel>
    </div>
    {rows.length === 0 ? <EmptyState title="No Marketplace listings" description="Complete supplier qualification and link an enabled quality policy before opening a release review." /> : <Panel title="Third-party Provider publications" description="Legacy listing uptime, verification, and price declarations remain visible but do not pass launch gates."><DataTable rows={rows} rowKey={(row) => row.id} columns={[
      { key: 'provider', label: 'Provider', render: (row) => <button className="table-link" onClick={() => setSelectedID(row.id)}><strong>{row.provider_name}</strong><small>{row.endpoint}</small></button> },
      { key: 'models', label: 'Declared models', render: (row) => row.supported_models.join(', ') || '—' },
      { key: 'status', label: 'Publication', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'verified', label: 'Legacy declaration', render: (row) => <StatusBadge value={row.verified ? 'DECLARED' : 'UNVERIFIED'} /> },
    ]} /></Panel>}
    {selected && <>
      <Panel title={`${selected.provider_name} release review`} description="A second administrator must approve only after every automated and drill gate has passed." action={!review || !['DRAFT', 'IN_REVIEW'].includes(review.status) ? <Button variant="primary" onClick={() => createReview.mutate()} disabled={createReview.isPending}><Rocket size={14} />Open new revision</Button> : undefined}>
        {reviews.isLoading ? <Skeleton rows={5} /> : !review ? <EmptyState title="No release review" description="Opening a review creates the complete versioned acceptance checklist." /> : <div className="marketplace-review-summary"><div><span>Review</span><strong>Revision {review.revision}</strong><small>{review.policy_version}</small></div><div><span>Status</span><StatusBadge value={review.status} /></div><div><span>Gate progress</span><strong>{review.passed_gate_count} / {review.gate_count}</strong><small>Approved {formatDate(review.approved_at)}</small></div><Button onClick={() => evaluate.mutate()} disabled={evaluate.isPending || review.status !== 'IN_REVIEW'}><Play size={14} />Evaluate platform evidence</Button></div>}
      </Panel>
      {review && <Panel title="Acceptance gates" description="Automated gates query immutable platform quality, usage, wallet, ledger, refund, reconciliation, dispute, and settlement evidence. Only operational drills accept admin attestations."><div className="marketplace-gate-grid">{review.gates.map((gate) => <article key={gate.id}><header><StatusBadge value={gate.status} /><small>{gate.evidence_source}</small></header><strong>{gate.gate_code.replaceAll('_', ' ')}</strong><code>{gate.evidence_reference || 'No platform evidence yet'}</code>{gate.evidence_source === 'ADMIN_ATTESTATION' && gate.status !== 'PASSED' && <div><input value={attestations[gate.gate_code] || ''} maxLength={300} placeholder="Drill ticket / report reference" onChange={(event) => setAttestations({ ...attestations, [gate.gate_code]: event.target.value })} /><Button size="sm" onClick={() => attest.mutate(gate)} disabled={!attestations[gate.gate_code] || !reason.trim()}>Attest</Button></div>}</article>)}</div></Panel>}
      {review && <Panel title="Production payout readiness" description="A production adapter remains blocked at approval, queue claim, completion, and database transition until all four independent reviews pass."><Form className="form-grid panel-pad" onSubmit={() => saveReadiness.mutateAsync()}>{readinessFields.map(([key, label]) => <div className="readiness-field" key={key}><label><span>{label}</span><select value={readiness[`${key}_status`]} onChange={(event) => setReadiness({ ...readiness, [`${key}_status`]: event.target.value })}><option>PENDING</option><option>APPROVED</option><option>REJECTED</option></select></label><label><span>Evidence reference</span><input value={readiness[`${key}_evidence_reference`]} onChange={(event) => setReadiness({ ...readiness, [`${key}_evidence_reference`]: event.target.value })} /></label></div>)}<div className="full-span inline-note"><ShieldCheck size={14} /><span>Production payout: <strong>{readiness.production_payout_enabled ? 'ENABLED' : 'BLOCKED'}</strong></span></div><SubmitButton pending={saveReadiness.isPending} disabled={!reason.trim()}>Save four-part review</SubmitButton></Form></Panel>}
      {review && <Panel title="Canary and lifecycle control" description="Canary requires the six foundation gates and a 1–2,000 bps platform quality cap. Emergency cutover is immediate and audited."><div className="form-grid panel-pad"><label className="full-span"><span>Required operator reason</span><input value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} /></label><div className="header-actions full-span"><Button onClick={() => lifecycle.mutate('CANARY_START')} disabled={!reason.trim() || review.status !== 'IN_REVIEW'}><Play size={14} />Start canary</Button><Button variant="primary" onClick={() => approve.mutate()} disabled={!reason.trim() || review.status !== 'IN_REVIEW' || review.passed_gate_count !== review.gate_count}><CheckCircle2 size={14} />Approve release</Button><Button onClick={() => lifecycle.mutate('SUSPEND')} disabled={!reason.trim()}>Suspend supplier</Button><Button variant="danger" onClick={() => lifecycle.mutate('EMERGENCY_CUTOVER')} disabled={!reason.trim()}><AlertOctagon size={14} />Emergency cutover</Button><Button onClick={() => lifecycle.mutate('RESUME')} disabled={!reason.trim()}>Resume</Button><Button variant="danger" onClick={() => lifecycle.mutate('EXIT')} disabled={!reason.trim()}>Exit supplier</Button></div></div></Panel>}
    </>}
  </div>
}
