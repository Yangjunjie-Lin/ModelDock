import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Check, FileCheck2, Plus, ShieldAlert, X } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import { Badge, Button, DataTable, type Column, EmptyState, ErrorState, StatusBadge } from '../components/ui'

type Report = { id: string; report_type: string; description: string; status: string; due_at: string }
type Policy = { id: string; phase: string; action: string; failure_mode: string; provider_name: string }
type Review = { id: string; reason: string; status: string; due_at?: string; request_id?: string }
type RiskEvent = { id: string; event_type: string; user_id?: string; organization_id?: string; score_delta: number; created_at: string }

export function GovernancePage() {
  const client = useQueryClient()
  const reports = useQuery({ queryKey: ['governance-reports'], queryFn: () => api<unknown>('/reports?limit=100').then(asPage<Report>) })
  const policies = useQuery({ queryKey: ['governance-policies'], queryFn: () => api<unknown>('/content-policies?limit=100').then(asPage<Policy>) })
  const reviews = useQuery({ queryKey: ['governance-reviews'], queryFn: () => api<unknown>('/manual-reviews?limit=100').then(asPage<Review>) })
  const riskEvents = useQuery({ queryKey: ['governance-risk-events'], queryFn: () => api<unknown>('/risk/events?limit=100').then(asPage<RiskEvent>) })
  const [policy, setPolicy] = useState({ organization_id: '', model_id: '', phase: 'PRE_REQUEST', action: 'REVIEW', failure_mode: 'FAIL_CLOSED', provider_name: 'builtin' })
  const createPolicy = useMutation({
    mutationFn: () => api('/content-policies', { method: 'POST', body: JSON.stringify(policy) }),
    onSuccess: () => {
      setPolicy({ organization_id: '', model_id: '', phase: 'PRE_REQUEST', action: 'REVIEW', failure_mode: 'FAIL_CLOSED', provider_name: 'builtin' })
      void client.invalidateQueries({ queryKey: ['governance-policies'] })
    },
  })
  const updateReview = useMutation({
    mutationFn: ({ id, status }: { id: string; status: 'APPROVED' | 'REJECTED' }) => api(`/manual-reviews/${id}`, { method: 'PATCH', body: JSON.stringify({ status }) }),
    onSuccess: () => void client.invalidateQueries({ queryKey: ['governance-reviews'] }),
  })
  const updateReport = useMutation({
    mutationFn: (id: string) => api(`/reports/${id}`, { method: 'PATCH', body: JSON.stringify({ status: 'RESOLVED' }) }),
    onSuccess: () => void client.invalidateQueries({ queryKey: ['governance-reports'] }),
  })
  const reportColumns: Column<Report>[] = [
    { key: 'type', label: 'Type', render: (row) => <Badge tone="violet">{row.report_type}</Badge> },
    { key: 'description', label: 'Description', render: (row) => <span>{row.description}</span> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'due', label: 'SLA due', render: (row) => <span>{formatDate(row.due_at)}</span> },
    { key: 'action', label: '', render: (row) => row.status !== 'RESOLVED' ? <Button size="sm" onClick={() => updateReport.mutate(row.id)}><Check size={14} />Resolve</Button> : null },
  ]
  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><ShieldAlert size={15} /> GOVERNANCE</div><h1>Risk & content governance</h1><p>Risk controls, content policy decisions, and complaints. Regulatory fields require legal review.</p></div></div>
    <div className="security-card tenant-safety"><AlertTriangle size={20} /><div><strong>Fail-closed content policy default</strong><p>Policy provider outages block requests unless deployment explicitly sets FAIL_OPEN.</p></div><Badge tone="warning">Legal review required</Badge></div>
    <section className="resource-panel"><div className="panel-heading"><div><strong>Manual review queue</strong><small>Content decisions waiting for an accountable operator</small></div><FileCheck2 size={18} /></div>{reviews.isLoading && <div className="panel-pad">Loading reviews...</div>}{reviews.isError && <ErrorState error={reviews.error} onRetry={() => reviews.refetch()} />}{reviews.isSuccess && (reviews.data.items.length ? <DataTable columns={[
      { key: 'reason', label: 'Reason', render: (row) => row.reason },
      { key: 'request', label: 'Request', render: (row) => <code>{row.request_id || 'Not linked'}</code> },
      { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
      { key: 'due', label: 'Due', render: (row) => formatDate(row.due_at) },
      { key: 'actions', label: '', render: (row) => row.status === 'PENDING' || row.status === 'IN_REVIEW' ? <div className="button-row"><Button size="sm" onClick={() => updateReview.mutate({ id: row.id, status: 'APPROVED' })}><Check size={14} />Approve</Button><Button size="sm" variant="danger" onClick={() => updateReview.mutate({ id: row.id, status: 'REJECTED' })}><X size={14} />Reject</Button></div> : null },
    ]} rows={reviews.data.items} rowKey={(row) => row.id} /> : <EmptyState title="No pending reviews" />)}</section>
    <section className="resource-panel"><div className="panel-heading"><div><strong>Reports and complaints</strong><small>API key abuse, content, order, charge, and privacy reports</small></div></div>{reports.isLoading && <div className="panel-pad">Loading reports...</div>}{reports.isError && <ErrorState error={reports.error} onRetry={() => reports.refetch()} />}{reports.isSuccess && (reports.data.items.length ? <DataTable columns={reportColumns} rows={reports.data.items} rowKey={(row) => row.id} /> : <EmptyState title="No reports" />)}</section>
    <section className="resource-panel"><div className="panel-heading"><div><strong>Content policies</strong><small>Pre-request, provider-native, and post-response hooks</small></div><FileCheck2 size={18} /></div><div className="panel-pad form-grid"><label><span>Organization ID</span><input value={policy.organization_id} onChange={(event) => setPolicy({ ...policy, organization_id: event.target.value })} /></label><label><span>Model ID</span><input value={policy.model_id} onChange={(event) => setPolicy({ ...policy, model_id: event.target.value })} /></label><label><span>Phase</span><select value={policy.phase} onChange={(event) => setPolicy({ ...policy, phase: event.target.value })}><option>PRE_REQUEST</option><option>PROVIDER_NATIVE</option><option>POST_RESPONSE</option></select></label><label><span>Action</span><select value={policy.action} onChange={(event) => setPolicy({ ...policy, action: event.target.value })}><option>ALLOW</option><option>BLOCK</option><option>REVIEW</option><option>REDACT</option></select></label><label><span>Failure mode</span><select value={policy.failure_mode} onChange={(event) => setPolicy({ ...policy, failure_mode: event.target.value })}><option>FAIL_CLOSED</option><option>FAIL_OPEN</option></select></label><label><span>Provider</span><input value={policy.provider_name} onChange={(event) => setPolicy({ ...policy, provider_name: event.target.value })} /></label><Button variant="primary" disabled={createPolicy.isPending || (!policy.organization_id.trim() && !policy.model_id.trim())} onClick={() => createPolicy.mutate()}><Plus size={15} />Create reviewed policy</Button></div>{policies.isSuccess && (policies.data.items.length ? <DataTable columns={[{ key: 'phase', label: 'Phase', render: (row) => row.phase }, { key: 'action', label: 'Action', render: (row) => <Badge tone="violet">{row.action}</Badge> }, { key: 'failure', label: 'Failure mode', render: (row) => row.failure_mode }, { key: 'provider', label: 'Provider', render: (row) => row.provider_name }]} rows={policies.data.items} rowKey={(row) => row.id} /> : <EmptyState title="No policies configured" />)}</section>
    <section className="resource-panel"><div className="panel-heading"><div><strong>Risk signal history</strong><small>Hashed IP/device anomaly and abuse events</small></div><ShieldAlert size={18} /></div>{riskEvents.isSuccess && (riskEvents.data.items.length ? <DataTable columns={[{ key: 'event', label: 'Event', render: (row) => row.event_type }, { key: 'subject', label: 'Subject', render: (row) => <code>{row.organization_id || row.user_id || 'Unscoped'}</code> }, { key: 'score', label: 'Score change', render: (row) => row.score_delta }, { key: 'time', label: 'Created', render: (row) => formatDate(row.created_at) }]} rows={riskEvents.data.items} rowKey={(row) => row.id} /> : <EmptyState title="No risk events" />)}</section>
  </div>
}
