import {
  type ButtonHTMLAttributes,
  createContext,
  type FormEvent,
  type InputHTMLAttributes,
  type ReactNode,
  type CSSProperties,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'
import { AlertCircle, Check, ChevronLeft, ChevronRight, Inbox, LoaderCircle, Search, X } from 'lucide-react'

export function Button({ className = '', variant = 'default', size = 'md', children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'default' | 'primary' | 'danger' | 'ghost'; size?: 'sm' | 'md' }) {
  return <button className={`button button-${variant} button-${size} ${className}`} {...props}>{children}</button>
}

export function Badge({ children, value, tone = 'neutral', dot = false }: { children?: ReactNode; value?: ReactNode; tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'violet'; dot?: boolean }) {
  return <span className={`badge badge-${tone}`}>{dot && <span className="badge-dot" />}{children ?? value}</span>
}

export function StatusBadge({ value }: { value?: unknown }) {
  const status = String(value || 'unknown').toLowerCase().replaceAll('_', ' ')
  const tone = status.includes('active') || status.includes('healthy') || status.includes('success') || status.includes('delivered') || status === 'enabled' || status === '200'
    ? 'success'
    : status.includes('cooldown') || status.includes('rate') || status.includes('pending') || status.includes('warning') || status.includes('retry') || status.includes('grace')
      ? 'warning'
      : status.includes('fail') || status.includes('error') || status.includes('revoked') || status.includes('unhealthy') || status.includes('disabled') || status.includes('archived') || status.includes('suspended') || status.includes('expired') || status.includes('terminated') || status.includes('stopped') || status === 'dead' || status.includes('exceeded') || status.includes('blocked')
        ? 'danger'
        : 'neutral'
  return <Badge tone={tone} dot>{status}</Badge>
}

export function Panel({ children, className = '', title, description, action }: { children: ReactNode; className?: string; title?: string; description?: string; action?: ReactNode }) {
  return (
    <section className={`panel ${className}`}>
      {(title || action) && (
        <div className="panel-heading">
          <div><h2>{title}</h2>{description && <p>{description}</p>}</div>
          {action}
        </div>
      )}
      {children}
    </section>
  )
}

export function Skeleton({ rows = 4 }: { rows?: number }) {
  return <div className="skeleton-stack" aria-label="Loading">{Array.from({ length: rows }).map((_, index) => <div className="skeleton-line" style={{ width: `${95 - (index % 3) * 12}%` }} key={index} />)}</div>
}

export function EmptyState({ title = 'No data yet', description, action }: { title?: string; description?: string; action?: ReactNode }) {
  return <div className="empty-state"><span className="empty-icon"><Inbox size={20} /></span><h3>{title}</h3>{description && <p>{description}</p>}{action}</div>
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const message = error instanceof Error ? error.message : 'The service did not return a usable response.'
  return <div className="error-state"><AlertCircle size={19} /><div><strong>Unable to load data</strong><p>{message}</p></div>{onRetry && <Button size="sm" onClick={onRetry}>Retry</Button>}</div>
}

export function SearchInput({ value, onChange, placeholder = 'Search…', ...props }: Omit<InputHTMLAttributes<HTMLInputElement>, 'onChange'> & { value: string; onChange: (value: string) => void }) {
  return <label className="search-input"><Search size={15} /><input value={value} placeholder={placeholder} onChange={(event) => onChange(event.target.value)} {...props} />{value && <button type="button" aria-label="Clear search" onClick={() => onChange('')}><X size={14} /></button>}</label>
}

export function Pagination({ page, pageSize = 20, total, onChange }: { page: number; pageSize?: number; total: number; onChange: (page: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return <div className="pagination"><span>{total ? `${(page - 1) * pageSize + 1}–${Math.min(page * pageSize, total)} of ${total}` : '0 results'}</span><div><Button variant="ghost" size="sm" disabled={page <= 1} onClick={() => onChange(page - 1)} aria-label="Previous page"><ChevronLeft size={15} /></Button><span>{`Page ${page} of ${pages}`}</span><Button variant="ghost" size="sm" disabled={page >= pages} onClick={() => onChange(page + 1)} aria-label="Next page"><ChevronRight size={15} /></Button></div></div>
}

export type Column<T> = { key: string; label: string; className?: string; render: (row: T) => ReactNode }

export function DataTable<T>({ columns, rows, rowKey, selected, onSelect }: { columns: Column<T>[]; rows: T[]; rowKey: (row: T) => string; selected?: Set<string>; onSelect?: (id: string, checked: boolean) => void }) {
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead><tr>{onSelect && <th className="check-cell"><span className="sr-only">Select</span></th>}{columns.map((column) => <th key={column.key} className={column.className}>{column.label}</th>)}</tr></thead>
        <tbody>{rows.map((row) => { const key = rowKey(row); return <tr key={key}>{onSelect && <td className="check-cell"><input type="checkbox" checked={selected?.has(key) || false} onChange={(event) => onSelect(key, event.target.checked)} /></td>}{columns.map((column) => <td key={column.key} className={column.className}>{column.render(row)}</td>)}</tr> })}</tbody>
      </table>
    </div>
  )
}

export function Modal({ open, onClose, title, description, children, footer, wide = false }: { open: boolean; onClose: () => void; title: string; description?: string; children: ReactNode; footer?: ReactNode; wide?: boolean }) {
  useEffect(() => {
    if (!open) return
    const handle = (event: KeyboardEvent) => event.key === 'Escape' && onClose()
    document.addEventListener('keydown', handle)
    document.body.style.overflow = 'hidden'
    return () => { document.removeEventListener('keydown', handle); document.body.style.overflow = '' }
  }, [open, onClose])
  if (!open) return null
  return <div className="overlay" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}><div className={`modal ${wide ? 'modal-wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}><div className="modal-head"><div><h2>{title}</h2>{description && <p>{description}</p>}</div><button onClick={onClose} aria-label="Close"><X size={18} /></button></div><div className="modal-body">{children}</div>{footer && <div className="modal-footer">{footer}</div>}</div></div>
}

export function Drawer({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: string; children: ReactNode }) {
  if (!open) return null
  return <div className="overlay drawer-overlay" onMouseDown={(event) => event.target === event.currentTarget && onClose()}><aside className="drawer"><div className="modal-head"><h2>{title}</h2><button onClick={onClose} aria-label="Close"><X size={18} /></button></div><div className="modal-body">{children}</div></aside></div>
}

type Toast = { id: number; message: string; tone: 'success' | 'danger' }
const ToastContext = createContext<(message: string, tone?: Toast['tone']) => void>(() => undefined)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const push = useCallback((message: string, tone: Toast['tone'] = 'success') => {
    const id = Date.now()
    setToasts((current) => [...current, { id, message, tone }])
    window.setTimeout(() => setToasts((current) => current.filter((toast) => toast.id !== id)), 3500)
  }, [])
  return <ToastContext.Provider value={push}>{children}<div className="toast-stack" aria-live="polite">{toasts.map((toast) => <div className={`toast toast-${toast.tone}`} key={toast.id}>{toast.tone === 'success' ? <Check size={16} /> : <AlertCircle size={16} />}{toast.message}</div>)}</div></ToastContext.Provider>
}

export const useToast = () => useContext(ToastContext)

export function SubmitButton({ pending, children, disabled, ...props }: { pending?: boolean; children: ReactNode } & Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'>) {
  return <Button type="submit" variant="primary" disabled={Boolean(pending || disabled)} {...props}>{pending && <LoaderCircle className="spin" size={15} />}{children}</Button>
}

export function Form({ onSubmit, children, className = '', id }: { onSubmit: () => unknown | Promise<unknown>; children: ReactNode; className?: string; id?: string }) {
  const handle = (event: FormEvent) => { event.preventDefault(); void onSubmit() }
  return <form id={id} className={className} onSubmit={handle}>{children}</form>
}

export function Metric({ label, value, hint, icon }: { label: string; value: ReactNode; hint?: ReactNode; icon?: ReactNode }) {
  return <div className="metric"><div className="metric-top"><span>{label}</span>{icon}</div><strong>{value}</strong>{hint && <div className="metric-hint">{hint}</div>}</div>
}

export function Segmented<T extends string>({ value, options, onChange }: { value: T; options: Array<{ label: string; value: T }>; onChange: (value: T) => void }) {
  const selected = useMemo(() => options.findIndex((option) => option.value === value), [options, value])
  return <div className="segmented" style={{ '--selected': selected } as CSSProperties}>{options.map((option) => <button key={option.value} type="button" className={option.value === value ? 'active' : ''} onClick={() => onChange(option.value)}>{option.label}</button>)}</div>
}
