import { type ReactNode, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDownUp, Download, Filter, MoreHorizontal, Plus, SlidersHorizontal } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import type { Row } from '../lib/types'
import { Badge, Button, type Column, DataTable, Drawer, EmptyState, ErrorState, Form, Modal, Pagination, SearchInput, Segmented, Skeleton, SubmitButton, useToast } from './ui'

export interface FieldConfig {
  name: string
  label: string
  type?: 'text' | 'number' | 'url' | 'password' | 'select' | 'textarea'
  required?: boolean
  placeholder?: string
  options?: Array<{ value: string; label: string }>
  hint?: string
}

export interface ResourceConfig {
  title: string
  description: string
  endpoint: string
  noun: string
  columns: Array<{ key: string; label: string; render?: (row: Row) => ReactNode; className?: string }>
  createFields?: FieldConfig[]
  createLabel?: string
  filters?: Array<{ value: string; label: string }>
  sort?: Array<{ value: string; label: string }>
  emptyTitle?: string
  emptyDescription?: string
  headerExtra?: ReactNode
  periodFilter?: boolean
  rowActions?: (row: Row) => ReactNode
}

export function ResourcePage({ config }: { config: ResourceConfig }) {
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState('')
  const [sort, setSort] = useState(config.sort?.[0]?.value || '-created_at')
  const [period, setPeriod] = useState<'today' | '7d' | '30d'>('7d')
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [detail, setDetail] = useState<Row | null>(null)
  const [form, setForm] = useState<Record<string, string>>({})
  const [createdSecret, setCreatedSecret] = useState('')
  const queryClient = useQueryClient()
  const toast = useToast()
  const result = useQuery({
    queryKey: ['resource', config.endpoint, search, status, sort, period, page],
    queryFn: () => api<unknown>(config.endpoint, { query: { search, status, sort, days: config.periodFilter ? (period === 'today' ? 1 : period === '7d' ? 7 : 30) : undefined, limit: 20, offset: (page - 1) * 20 } }).then(asPage<Row>),
  })
  const create = useMutation({
    mutationFn: () => api<Row>(config.endpoint, { method: 'POST', body: JSON.stringify(normalizeForm(form, config.createFields || [])) }),
    onSuccess: (value) => {
      const secret = String(value.key || value.api_key || value.secret || '')
      setCreateOpen(false); setForm({}); void queryClient.invalidateQueries({ queryKey: ['resource', config.endpoint] })
      if (secret) setCreatedSecret(secret)
      toast(`${config.noun} created`)
    },
  })
  const rows = result.data?.items || []
  const columns = useMemo<Column<Row>[]>(() => [
    ...config.columns.map((column) => ({
      ...column,
      render: column.render || ((row: Row) => <span>{display(row[column.key])}</span>),
    })),
    { key: '_actions', label: '', className: 'action-cell', render: (row: Row) => <div className="row-actions">{config.rowActions?.(row)}<Button variant="ghost" size="sm" onClick={() => setDetail(row)} aria-label={`View ${config.noun}`}><MoreHorizontal size={16} /></Button></div> },
  ], [config])

  const exportMetadata = () => {
    const safe = rows.map((row) => Object.fromEntries(Object.entries(row).filter(([key]) => !isSensitive(key))))
    const blob = new Blob([JSON.stringify(safe, null, 2)], { type: 'application/json' })
    const link = document.createElement('a'); link.href = URL.createObjectURL(blob); link.download = `relaydock-${config.noun.toLowerCase().replaceAll(' ', '-')}-metadata.json`; link.click(); URL.revokeObjectURL(link.href)
  }

  return (
    <div className="page-stack">
      <div className="page-header"><div><h1>{config.title}</h1><p>{config.description}</p></div><div className="header-actions">{config.headerExtra}<Button onClick={exportMetadata}><Download size={15} />Export</Button>{config.createFields && <Button variant="primary" onClick={() => setCreateOpen(true)}><Plus size={15} />{config.createLabel || `Add ${config.noun}`}</Button>}</div></div>
      <section className="resource-panel">
        {config.periodFilter && <div className="period-row"><Segmented value={period} onChange={(value) => { setPeriod(value); setPage(1) }} options={[{ value: 'today', label: 'Today' }, { value: '7d', label: '7 days' }, { value: '30d', label: '30 days' }]} /><span>Estimated cost uses RelayDock configured pricing.</span></div>}
        <div className="resource-toolbar"><SearchInput value={search} onChange={(value) => { setSearch(value); setPage(1) }} placeholder={`Search ${config.title.toLowerCase()}…`} /><div className="toolbar-controls">{config.filters && <label className="select-control"><Filter size={14} /><select value={status} onChange={(event) => { setStatus(event.target.value); setPage(1) }}><option value="">All statuses</option>{config.filters.map((filter) => <option value={filter.value} key={filter.value}>{filter.label}</option>)}</select></label>}{config.sort && <label className="select-control"><ArrowDownUp size={14} /><select value={sort} onChange={(event) => setSort(event.target.value)}>{config.sort.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}</select></label>}<Button size="sm"><SlidersHorizontal size={14} />Filters</Button></div></div>
        {result.isLoading && <Skeleton rows={8} />}
        {result.isError && <div className="panel-pad"><ErrorState error={result.error} onRetry={() => result.refetch()} /></div>}
        {result.isSuccess && rows.length === 0 && <EmptyState title={config.emptyTitle || `No ${config.title.toLowerCase()}`} description={config.emptyDescription || `No records matched the current filters.`} action={config.createFields && <Button variant="primary" onClick={() => setCreateOpen(true)}><Plus size={15} />{config.createLabel || `Add ${config.noun}`}</Button>} />}
        {rows.length > 0 && <DataTable columns={columns} rows={rows} rowKey={(row) => String(row.id || row.request_id || JSON.stringify(row))} />}
        <Pagination page={page} total={result.data?.total || 0} onChange={setPage} />
      </section>
      <Modal open={createOpen} onClose={() => setCreateOpen(false)} title={config.createLabel || `Add ${config.noun}`} description={`Create a new ${config.noun.toLowerCase()} in RelayDock.`} footer={<><Button onClick={() => setCreateOpen(false)}>Cancel</Button><SubmitButton form="resource-create-form" pending={create.isPending}>Create {config.noun}</SubmitButton></>}>
        <Form id="resource-create-form" className="form-grid" onSubmit={() => create.mutateAsync()}>
          {(config.createFields || []).map((field) => <FormField key={field.name} field={field} value={form[field.name] || ''} onChange={(value) => setForm((current) => ({ ...current, [field.name]: value }))} />)}
          {create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Unable to create this record.'}</div>}
        </Form>
      </Modal>
      <Modal open={Boolean(createdSecret)} onClose={() => setCreatedSecret('')} title="Copy your API key" description="This secret is shown once. Store it in a secure secret manager.">
        <div className="secret-box"><code>{createdSecret}</code><Button onClick={() => { void navigator.clipboard.writeText(createdSecret); toast('API key copied') }}>Copy</Button></div><div className="inline-warning">For security, RelayDock stores only a hash. You will not be able to retrieve this value later.</div>
      </Modal>
      <Drawer open={Boolean(detail)} onClose={() => setDetail(null)} title={`${config.noun} details`}>
        {detail && <div className="detail-list">{Object.entries(detail).filter(([key]) => !isSensitive(key)).map(([key, value]) => <div key={key}><span>{humanize(key)}</span><strong>{typeof value === 'object' ? JSON.stringify(value) : display(value)}</strong></div>)}</div>}
      </Drawer>
    </div>
  )
}

function FormField({ field, value, onChange }: { field: FieldConfig; value: string; onChange: (value: string) => void }) {
  const input = field.type === 'select'
    ? <select value={value} required={field.required} onChange={(event) => onChange(event.target.value)}><option value="">Select…</option>{field.options?.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}</select>
    : field.type === 'textarea'
      ? <textarea value={value} required={field.required} placeholder={field.placeholder} onChange={(event) => onChange(event.target.value)} rows={4} />
      : <input value={value} required={field.required} type={field.type || 'text'} placeholder={field.placeholder} onChange={(event) => onChange(event.target.value)} />
  return <label className={field.type === 'textarea' ? 'full-span' : ''}><span>{field.label}{field.required && <b> *</b>}</span>{input}{field.hint && <small>{field.hint}</small>}</label>
}

function normalizeForm(form: Record<string, string>, fields: FieldConfig[]) {
  return Object.fromEntries(Object.entries(form).map(([key, value]) => [key, fields.find((field) => field.name === key)?.type === 'number' ? Number(value) : value]))
}

function display(value: unknown) {
  if (value === null || value === undefined || value === '') return '—'
  if (typeof value === 'boolean') return value ? 'Yes' : 'No'
  if (Array.isArray(value)) return value.join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function isSensitive(key: string) {
  return /(secret|password|authorization|cookie|key_hash|encrypted|prompt|content|request_body|response_body)/i.test(key)
}

function humanize(value: string) {
  return value.replaceAll('_', ' ').replace(/\b\w/g, (character) => character.toUpperCase())
}

export const cell = {
  primary: (key: string, secondary?: string) => (row: Row) => <div className="primary-cell"><strong>{display(row[key])}</strong>{secondary && <small>{display(row[secondary])}</small>}</div>,
  date: (key: string) => (row: Row) => <span className="muted-cell">{formatDate(row[key])}</span>,
  tags: (key: string) => (row: Row) => <div className="badge-row">{(Array.isArray(row[key]) ? row[key] as unknown[] : []).map((tag) => <Badge key={String(tag)}>{String(tag)}</Badge>)}</div>,
}
