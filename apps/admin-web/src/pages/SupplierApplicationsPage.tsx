import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, RefreshCw, ShieldAlert, XCircle } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import { Badge, Button, DataTable, EmptyState, ErrorState, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type Supplier = {
  id: string
  legal_name: string
  display_name: string
  incorporation_country: string
  kyb_status: string
  contract_status: string
  status: string
  endpoints?: Array<{ verification_status: string; isolation_status: string }>
  questionnaires?: Array<{ id: string; status: string }>
  residency?: Array<{ id: string; processing_regions: string[]; storage_regions: string[]; status: string }>
  models?: Array<{ id: string; model_name: string; model_type: string; status: string }>
  prices?: Array<{ id: string; model_application_id: string; input_token_price: string; output_token_price: string; currency: string; unit: number; status: string }>
  updated_at: string
}

export function SupplierApplicationsPage() {
  const [reviewing, setReviewing] = useState<Supplier | null>(null)
  const [decision, setDecision] = useState('REQUESTED_CHANGES')
  const [reason, setReason] = useState('')
  const [status, setStatus] = useState('')
  const client = useQueryClient()
  const toast = useToast()
  const suppliers = useQuery({ queryKey: ['supplier-applications', status], queryFn: () => api<unknown>('/suppliers', { query: { status, limit: 100 } }).then(asPage<Supplier>) })
  const detail = useQuery({ queryKey: ['supplier-application', reviewing?.id], queryFn: () => api<Supplier>(`/suppliers/${reviewing?.id}`), enabled: Boolean(reviewing?.id) })
  const review = useMutation({ mutationFn: () => api(`/suppliers/${reviewing?.id}/review`, { method: 'PATCH', body: JSON.stringify({ decision, reason }) }), onSuccess: () => { setReviewing(null); setReason(''); void client.invalidateQueries({ queryKey: ['supplier-applications'] }); toast('Supplier review recorded') } })
  const setCompliance = useMutation({ mutationFn: (payload: Record<string, string>) => api(`/suppliers/${reviewing?.id}/compliance`, { method: 'PATCH', body: JSON.stringify(payload) }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['supplier-applications'] }); toast('Compliance status updated') } })
  const reviewEvidence = useMutation({ mutationFn: ({ type, id, status: nextStatus }: { type: string; id: string; status: string }) => api(`/supplier-evidence/${type}/${id}`, { method: 'PATCH', body: JSON.stringify({ status: nextStatus, reason: reason || 'Reviewed in supplier governance console' }) }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['supplier-application', reviewing?.id] }); toast('Evidence review recorded') } })

  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><ShieldAlert size={14} />SUPPLIER GOVERNANCE</div><h1>Supplier applications</h1><p>Review evidence before a supplier can be connected to a production Provider. Supplier-submitted status changes cannot approve an application.</p></div><div className="header-actions"><Button onClick={() => void suppliers.refetch()}><RefreshCw size={14} />Refresh</Button></div></div>
    <Panel title="Applications" description="Approval requires verified KYB, an active contract, a passed public-endpoint isolation check, and an approved security questionnaire.">
      <div className="toolbar"><label><span>Status</span><select value={status} onChange={(event) => setStatus(event.target.value)}><option value="">All statuses</option>{['DRAFT','SUBMITTED','IN_REVIEW','APPROVED','REJECTED','SUSPENDED','EXIT_REQUESTED','EXITED'].map((value) => <option key={value} value={value}>{value.replaceAll('_', ' ')}</option>)}</select></label></div>
      {suppliers.isLoading && <Skeleton rows={6} />}{suppliers.isError && <ErrorState error={suppliers.error} onRetry={() => suppliers.refetch()} />}{suppliers.isSuccess && suppliers.data.items.length === 0 && <EmptyState title="No supplier applications" description="Applications submitted from the supplier console will appear here." />}{suppliers.isSuccess && suppliers.data.items.length > 0 && <DataTable rows={suppliers.data.items} rowKey={(row) => row.id} columns={[{ key: 'supplier', label: 'Supplier', render: (row) => <div className="primary-cell"><strong>{row.display_name || row.legal_name}</strong><small>{row.legal_name} · {row.incorporation_country}</small></div> }, { key: 'status', label: 'Application', render: (row) => <StatusBadge value={row.status} /> }, { key: 'kyb', label: 'KYB / contract', render: (row) => <div className="primary-cell"><Badge value={row.kyb_status} /><small>{row.contract_status}</small></div> }, { key: 'evidence', label: 'Evidence', render: (row) => <div className="primary-cell"><small>{row.endpoints?.length || 0} endpoint(s)</small><small>{row.questionnaires?.length || 0} questionnaire(s)</small></div> }, { key: 'updated', label: 'Updated', render: (row) => formatDate(row.updated_at) }, { key: 'action', label: '', render: (row) => <Button size="sm" onClick={() => { setReviewing(row); setDecision(row.status === 'APPROVED' ? 'SUSPENDED' : 'REQUESTED_CHANGES') }}>Review</Button> }]} />}</Panel>
    <Modal open={Boolean(reviewing)} onClose={() => setReviewing(null)} title={reviewing ? `Review ${reviewing.display_name || reviewing.legal_name}` : 'Review supplier'} description="Every decision is written to the immutable audit log." wide>
      {reviewing && <div className="form-stack"><div className="inline-stats"><span><small>Current status</small><strong><StatusBadge value={reviewing.status} /></strong></span><span><small>KYB</small><strong>{reviewing.kyb_status}</strong></span><span><small>Contract</small><strong>{reviewing.contract_status}</strong></span></div><div className="form-grid"><label><span>KYB status</span><select defaultValue={reviewing.kyb_status} onChange={(event) => setCompliance.mutate({ kyb_status: event.target.value, contract_status: reviewing.contract_status, contract_version: '' })}>{['NOT_STARTED','PENDING','VERIFIED','REJECTED','EXPIRED'].map((value) => <option key={value}>{value}</option>)}</select></label><label><span>Contract status</span><select defaultValue={reviewing.contract_status} onChange={(event) => setCompliance.mutate({ kyb_status: reviewing.kyb_status, contract_status: event.target.value, contract_version: '' })}>{['NOT_STARTED','PENDING','ACTIVE','EXPIRED','TERMINATED'].map((value) => <option key={value}>{value}</option>)}</select></label></div><label><span>Decision</span><select value={decision} onChange={(event) => setDecision(event.target.value)}>{['APPROVED','REJECTED','REQUESTED_CHANGES','SUSPENDED','EXITED'].map((value) => <option key={value}>{value}</option>)}</select></label><label><span>Reason</span><textarea value={reason} onChange={(event) => setReason(event.target.value)} rows={4} placeholder="Record the evidence and decision rationale" /></label><div className="modal-footer"><Button onClick={() => setReviewing(null)}>Cancel</Button><SubmitButton pending={review.isPending} disabled={!reason.trim()} onClick={() => review.mutate()}>Record decision</SubmitButton></div>{review.isError && <p className="form-error">{String(review.error)}</p>}{decision === 'APPROVED' ? <p className="form-hint"><CheckCircle2 size={14} /> Approval is fail-closed until all required evidence is present.</p> : <p className="form-hint"><XCircle size={14} /> Suspended and exited suppliers are excluded from any future Provider linkage.</p>}</div>}
      {detail.isLoading && <Skeleton rows={4} />}{detail.data && <div className="form-stack"><h3>Submitted evidence</h3>{detail.data.questionnaires?.map((item, index) => <div className="inline-note" key={`questionnaire-${index}`}><span>Security questionnaire #{index + 1}</span><StatusBadge value={item.status} /><Button size="sm" onClick={() => reviewEvidence.mutate({ type: 'QUESTIONNAIRE', id: item.id, status: 'APPROVED' })}>Approve</Button></div>)}{detail.data.residency?.map((item) => <div className="inline-note" key={item.id}><span>Residency: {item.processing_regions.join(', ') || 'undisclosed'}</span><StatusBadge value={item.status} /><Button size="sm" onClick={() => reviewEvidence.mutate({ type: 'RESIDENCY', id: item.id, status: 'APPROVED' })}>Approve</Button></div>)}{detail.data.models?.map((item) => <div className="inline-note" key={item.id}><span>{item.model_name} · {item.model_type}</span><StatusBadge value={item.status} /><Button size="sm" onClick={() => reviewEvidence.mutate({ type: 'MODEL', id: item.id, status: 'APPROVED' })}>Approve</Button></div>)}{detail.data.prices?.map((item) => <div className="inline-note" key={item.id}><span>{item.currency} {item.input_token_price} / {item.output_token_price} per {item.unit}</span><StatusBadge value={item.status} /><Button size="sm" onClick={() => reviewEvidence.mutate({ type: 'PRICE', id: item.id, status: 'APPROVED' })}>Approve</Button></div>)}</div>}
    </Modal>
  </div>
}
