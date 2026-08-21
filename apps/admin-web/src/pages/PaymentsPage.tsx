import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, asPage, formatDate, formatMoney } from '../lib/api'
import type { Row } from '../lib/types'
import { Badge, Button, DataTable, EmptyState, ErrorState, Form, Modal, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

export function PaymentsPage() {
  const orders = useQuery({ queryKey: ['recharge-orders'], queryFn: () => api<unknown>('/recharge-orders').then(asPage<Row>), refetchInterval: 10_000 })
  return <div className="page-stack">
    <div className="page-header"><div><h1>Payments</h1><p>Review recharge evidence, query providers, refund credited orders, and reconcile the payment ledger.</p></div></div>
    <section className="resource-panel">
      {orders.isLoading && <Skeleton rows={7} />}
      {orders.isError && <div className="panel-pad"><ErrorState error={orders.error} onRetry={() => orders.refetch()} /></div>}
      {orders.isSuccess && !orders.data.items.length && <EmptyState title="No recharge orders" description="User-created payment orders will appear here." />}
      {!!orders.data?.items.length && <DataTable rows={orders.data.items} rowKey={(row) => String(row.id)} columns={[
        { key: 'order', label: 'Order', render: (row) => <div className="primary-cell"><strong>{String(row.platform_order_no)}</strong><small>{String(row.provider_order_no || 'No provider order')}</small></div> },
        { key: 'organization', label: 'Organization', render: (row) => <code>{String(row.organization_id)}</code> },
        { key: 'provider', label: 'Provider', render: (row) => <Badge tone="info">{String(row.payment_provider)}</Badge> },
        { key: 'amount', label: 'Amount', render: (row) => <strong>{formatMoney(row.amount, row.currency)}</strong> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'created', label: 'Created', render: (row) => formatDate(row.created_at) },
        { key: 'actions', label: '', render: (row) => <OrderActions order={row} /> },
      ]} />}
    </section>
  </div>
}

function OrderActions({ order }: { order: Row }) {
  const [approvalOpen, setApprovalOpen] = useState(false)
  const [refundOpen, setRefundOpen] = useState(false)
  const [evidence, setEvidence] = useState('')
  const [reason, setReason] = useState('')
  const client = useQueryClient()
  const toast = useToast()
  const refresh = () => client.invalidateQueries({ queryKey: ['recharge-orders'] })
  const action = useMutation({
    mutationFn: (kind: 'query' | 'reconcile') => api(`/recharge-orders/${String(order.id)}/${kind}`, { method: 'POST', body: '{}' }),
    onSuccess: (_, kind) => { void refresh(); toast(kind === 'query' ? 'Provider result queried' : 'Reconciliation recorded') },
  })
  const approve = useMutation({
    mutationFn: () => api(`/recharge-orders/${String(order.id)}/manual-approve`, { method: 'POST', body: JSON.stringify({ evidence_reference: evidence }) }),
    onSuccess: () => { setApprovalOpen(false); setEvidence(''); void refresh(); toast('Manual transfer approved and credited') },
  })
  const refund = useMutation({
    mutationFn: () => api(`/recharge-orders/${String(order.id)}/refunds`, { method: 'POST', body: JSON.stringify({ amount: order.amount, reason, idempotency_key: crypto.randomUUID() }) }),
    onSuccess: () => { setRefundOpen(false); setReason(''); void refresh(); toast('Refund workflow started') },
  })
  return <><div className="table-actions">
    <Button size="sm" onClick={() => action.mutate('query')} disabled={action.isPending}>Query</Button>
    <Button size="sm" onClick={() => action.mutate('reconcile')} disabled={action.isPending}>Reconcile</Button>
    {order.payment_provider === 'manual_transfer' && order.status === 'PENDING' && <Button size="sm" variant="primary" onClick={() => setApprovalOpen(true)}>Review</Button>}
    {order.status === 'CREDITED' && <Button size="sm" variant="danger" onClick={() => setRefundOpen(true)}>Refund</Button>}
  </div>
  <Modal open={approvalOpen} onClose={() => setApprovalOpen(false)} title="Approve manual transfer" description="Confirm independently reviewed, non-secret evidence. Approval atomically posts the wallet journal." footer={<><Button onClick={() => setApprovalOpen(false)}>Cancel</Button><SubmitButton form="manual-approval" pending={approve.isPending}>Approve and credit</SubmitButton></>}>
    <Form id="manual-approval" className="form-grid" onSubmit={() => approve.mutateAsync()}><label className="full-span"><span>Evidence reference</span><input required maxLength={200} value={evidence} onChange={(event) => setEvidence(event.target.value)} placeholder="Opaque internal evidence ID; no account or personal data" /></label>{approve.isError && <div className="form-error full-span">{approve.error instanceof Error ? approve.error.message : 'Approval failed.'}</div>}</Form>
  </Modal>
  <Modal open={refundOpen} onClose={() => setRefundOpen(false)} title="Refund payment" description={`Full refund of ${formatMoney(order.amount, order.currency)}. The provider result and wallet reversal are linked atomically.`} footer={<><Button onClick={() => setRefundOpen(false)}>Cancel</Button><SubmitButton form="payment-refund" pending={refund.isPending}>Start refund</SubmitButton></>}>
    <Form id="payment-refund" className="form-grid" onSubmit={() => refund.mutateAsync()}><label className="full-span"><span>Reason</span><textarea required rows={3} value={reason} onChange={(event) => setReason(event.target.value)} /></label>{refund.isError && <div className="form-error full-span">{refund.error instanceof Error ? refund.error.message : 'Refund failed.'}</div>}</Form>
  </Modal></>
}
