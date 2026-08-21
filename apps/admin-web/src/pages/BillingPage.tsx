import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { addDecimalStrings, api, asPage, formatDate, formatMoney, formatNumber } from '../lib/api'
import type { Row } from '../lib/types'
import { Badge, Button, DataTable, EmptyState, ErrorState, Form, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

export function BillingPage() {
  const wallets = useQuery({ queryKey: ['wallets'], queryFn: () => api<unknown>('/wallets').then(asPage<Row>) })
  const rows = wallets.data?.items || []
  return <div className="page-stack">
    <div className="page-header"><div><h1>Billing</h1><p>Manage prepaid balances, postpaid credit, top-ups, and the immutable transaction ledger.</p></div></div>
    <div className="metric-grid">
      <Panel title="Available balance"><strong className="billing-total">{formatMoney(addDecimalStrings(rows.map((row) => row.available_balance || '0')), rows[0]?.currency)}</strong></Panel>
      <Panel title="Reserved"><strong className="billing-total">{formatMoney(addDecimalStrings(rows.map((row) => row.reserved_balance || '0')), rows[0]?.currency)}</strong></Panel>
      <Panel title="Organizations"><strong className="billing-total">{formatNumber(rows.length, false)}</strong></Panel>
    </div>
    <section className="resource-panel">
      {wallets.isLoading && <Skeleton rows={7} />}
      {wallets.isError && <div className="panel-pad"><ErrorState error={wallets.error} onRetry={() => wallets.refetch()} /></div>}
      {wallets.isSuccess && rows.length === 0 && <EmptyState title="No wallets" description="A wallet is created automatically for every organization." />}
      {rows.length > 0 && <DataTable rows={rows} rowKey={(row) => String(row.id)} columns={[
        { key: 'organization_name', label: 'Organization', render: (row) => <div className="primary-cell"><strong>{String(row.organization_name || 'Organization')}</strong><small>{String(row.organization_id || '')}</small></div> },
        { key: 'billing_mode', label: 'Mode', render: (row) => <Badge tone="info">{String(row.billing_mode || 'POSTPAID')}</Badge> },
        { key: 'available_balance', label: 'Balance', render: (row) => <strong>{formatMoney(row.available_balance, row.currency)}</strong> },
        { key: 'credit_limit', label: 'Credit / risk limit', render: (row) => <div className="primary-cell"><strong>{formatMoney(row.credit_limit, row.currency)}</strong><small>Risk {formatMoney(row.risk_exposure, row.currency)} / {formatMoney(row.risk_limit, row.currency)}</small></div> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: '_actions', label: '', render: (row) => <div className="table-actions"><LedgerButton wallet={row} /><TopUpButton wallet={row} /></div> },
      ]} />}
    </section>
  </div>
}

function LedgerButton({ wallet }: { wallet: Row }) {
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<'operations' | 'journals'>('operations')
  const operations = useQuery({ queryKey: ['funding-operations', wallet.id], queryFn: () => api<unknown>(`/wallets/${String(wallet.id)}/funding-operations`).then(asPage<Row>), enabled: open })
  const journals = useQuery({ queryKey: ['ledger-journals', wallet.id], queryFn: () => api<unknown>(`/wallets/${String(wallet.id)}/journals`).then(asPage<Row>), enabled: open })
  const rows = tab === 'operations' ? operations.data?.items || [] : journals.data?.items || []
  return <><Button size="sm" onClick={() => setOpen(true)}>Ledger</Button><Modal open={open} onClose={() => setOpen(false)} title="Funding ledger" description="Immutable, balanced entries and request-level reservation settlements." wide footer={<Button onClick={() => setOpen(false)}>Close</Button>}><div className="settings-tabs"><Button size="sm" variant={tab === 'operations' ? 'primary' : undefined} onClick={() => setTab('operations')}>Operations</Button><Button size="sm" variant={tab === 'journals' ? 'primary' : undefined} onClick={() => setTab('journals')}>Journals</Button></div>{rows.length === 0 ? <EmptyState title="No ledger records" description="Funding operations appear when the gateway admits priced requests." /> : tab === 'operations' ? <DataTable rows={rows} rowKey={(row) => String(row.id)} columns={[
    { key: 'request_id', label: 'Request', render: (row) => <div className="primary-cell"><strong>{String(row.request_id)}</strong><small>{formatDate(row.created_at)}</small></div> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'maximum_amount', label: 'Reserved', render: (row) => formatMoney(row.maximum_amount, row.currency) },
    { key: 'settled_amount', label: 'Settled', render: (row) => formatMoney(row.settled_amount, row.currency) },
    { key: 'released_amount', label: 'Released', render: (row) => formatMoney(row.released_amount, row.currency) },
    { key: 'usage_source', label: 'Usage source', render: (row) => String(row.usage_source || '—') },
  ]} /> : <DataTable rows={rows} rowKey={(row) => String(row.id)} columns={[
    { key: 'external_key', label: 'Journal', render: (row) => <div className="primary-cell"><strong>{String(row.journal_type)}</strong><small>{String(row.external_key)}</small></div> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'reference', label: 'Reference', render: (row) => String(row.reference || '—') },
    { key: 'entries', label: 'Debit / credit', render: (row) => <div className="primary-cell">{Array.isArray(row.entries) ? row.entries.map((entry) => { const item = entry as Row; return <small key={String(item.id)}>{String(item.entry_side)} {formatMoney(item.amount, item.currency || row.currency)} · {String(item.account_name)}</small> }) : null}</div> },
    { key: 'created_at', label: 'Posted', render: (row) => formatDate(row.created_at) },
  ]} />}</Modal></>
}

function TopUpButton({ wallet }: { wallet: Row }) {
  const [open, setOpen] = useState(false)
  const [amount, setAmount] = useState('')
  const [reference, setReference] = useState('')
  const client = useQueryClient()
  const toast = useToast()
  const topup = useMutation({
    mutationFn: () => api(`/wallets/${String(wallet.id)}/topups`, { method: 'POST', body: JSON.stringify({ amount, reference, idempotency_key: crypto.randomUUID() }) }),
    onSuccess: () => { setOpen(false); setAmount(''); setReference(''); void client.invalidateQueries({ queryKey: ['wallets'] }); toast('Wallet topped up') },
  })
  return <><Button size="sm" onClick={() => setOpen(true)}><Plus size={13} />Top up</Button><Modal open={open} onClose={() => setOpen(false)} title="Top up wallet" description={`Add funds to ${String(wallet.organization_name || 'this organization')}.`} footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="wallet-topup" pending={topup.isPending}>Confirm top-up</SubmitButton></>}><Form id="wallet-topup" className="form-grid" onSubmit={() => topup.mutateAsync()}><label><span>Amount (USD) *</span><input type="number" min="0.00000001" step="0.01" required value={amount} onChange={(event) => setAmount(event.target.value)} /></label><label><span>Reference</span><input value={reference} onChange={(event) => setReference(event.target.value)} placeholder="Payment reference" /></label>{topup.isError && <div className="form-error full-span">{topup.error instanceof Error ? topup.error.message : 'Top-up failed.'}</div>}</Form></Modal></>
}
