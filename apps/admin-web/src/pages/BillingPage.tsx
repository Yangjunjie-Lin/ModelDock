import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { api, asPage, formatMoney, formatNumber } from '../lib/api'
import type { Row } from '../lib/types'
import { Badge, Button, DataTable, EmptyState, ErrorState, Form, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

export function BillingPage() {
  const wallets = useQuery({ queryKey: ['wallets'], queryFn: () => api<unknown>('/wallets').then(asPage<Row>) })
  const rows = wallets.data?.items || []
  return <div className="page-stack">
    <div className="page-header"><div><h1>Billing</h1><p>Manage prepaid balances, postpaid credit, top-ups, and the immutable transaction ledger.</p></div></div>
    <div className="metric-grid">
      <Panel title="Available balance"><strong className="billing-total">{formatMoney(rows.reduce((sum, row) => sum + Number(row.available_balance || 0), 0))}</strong></Panel>
      <Panel title="Reserved"><strong className="billing-total">{formatMoney(rows.reduce((sum, row) => sum + Number(row.reserved_balance || 0), 0))}</strong></Panel>
      <Panel title="Organizations"><strong className="billing-total">{formatNumber(rows.length, false)}</strong></Panel>
    </div>
    <section className="resource-panel">
      {wallets.isLoading && <Skeleton rows={7} />}
      {wallets.isError && <div className="panel-pad"><ErrorState error={wallets.error} onRetry={() => wallets.refetch()} /></div>}
      {wallets.isSuccess && rows.length === 0 && <EmptyState title="No wallets" description="A wallet is created automatically for every organization." />}
      {rows.length > 0 && <DataTable rows={rows} rowKey={(row) => String(row.id)} columns={[
        { key: 'organization_name', label: 'Organization', render: (row) => <div className="primary-cell"><strong>{String(row.organization_name || 'Organization')}</strong><small>{String(row.organization_id || '')}</small></div> },
        { key: 'billing_mode', label: 'Mode', render: (row) => <Badge tone="info">{String(row.billing_mode || 'POSTPAID')}</Badge> },
        { key: 'available_balance', label: 'Balance', render: (row) => <strong>{formatMoney(row.available_balance)}</strong> },
        { key: 'credit_limit', label: 'Credit limit', render: (row) => formatMoney(row.credit_limit) },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: '_actions', label: '', render: (row) => <TopUpButton wallet={row} /> },
      ]} />}
    </section>
  </div>
}

function TopUpButton({ wallet }: { wallet: Row }) {
  const [open, setOpen] = useState(false)
  const [amount, setAmount] = useState('')
  const [reference, setReference] = useState('')
  const client = useQueryClient()
  const toast = useToast()
  const topup = useMutation({
    mutationFn: () => api(`/wallets/${String(wallet.id)}/topups`, { method: 'POST', body: JSON.stringify({ amount: Number(amount), reference, idempotency_key: crypto.randomUUID() }) }),
    onSuccess: () => { setOpen(false); setAmount(''); setReference(''); void client.invalidateQueries({ queryKey: ['wallets'] }); toast('Wallet topped up') },
  })
  return <><Button size="sm" onClick={() => setOpen(true)}><Plus size={13} />Top up</Button><Modal open={open} onClose={() => setOpen(false)} title="Top up wallet" description={`Add funds to ${String(wallet.organization_name || 'this organization')}.`} footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="wallet-topup" pending={topup.isPending}>Confirm top-up</SubmitButton></>}><Form id="wallet-topup" className="form-grid" onSubmit={() => topup.mutateAsync()}><label><span>Amount (USD) *</span><input type="number" min="0.00000001" step="0.01" required value={amount} onChange={(event) => setAmount(event.target.value)} /></label><label><span>Reference</span><input value={reference} onChange={(event) => setReference(event.target.value)} placeholder="Payment reference" /></label>{topup.isError && <div className="form-error full-span">{topup.error instanceof Error ? topup.error.message : 'Top-up failed.'}</div>}</Form></Modal></>
}
