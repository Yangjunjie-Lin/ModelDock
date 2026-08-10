import { currentLocale } from './i18n'

const API_BASE = ((import.meta.env.VITE_API_BASE as string | undefined) || '/api/admin').replace(/\/$/, '')
const CSRF_COOKIE = 'relayedock_admin_csrf='

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code?: string,
  ) {
    super(message)
  }
}

type ApiOptions = RequestInit & { query?: Record<string, string | number | boolean | undefined> }

export async function api<T>(path: string, options: ApiOptions = {}): Promise<T> {
	return request<T>(path, options, true)
}

export async function apiDownload(path: string, filename: string, query: ApiOptions['query'] = {}): Promise<void> {
  const url = new URL(`${API_BASE}${path}`, window.location.origin)
  Object.entries(query ?? {}).forEach(([key, value]) => {
    if (value !== undefined && value !== '') url.searchParams.set(key, String(value))
  })
  let response = await fetch(url, { credentials: 'include', headers: { Accept: 'text/csv' } })
  if (response.status === 401) {
    const csrf = csrfToken()
    const refresh = await fetch(`${API_BASE}/auth/refresh`, { method: 'POST', credentials: 'include', headers: csrf ? { 'X-CSRF-Token': csrf } : {} })
    if (refresh.ok) response = await fetch(url, { credentials: 'include', headers: { Accept: 'text/csv' } })
  }
  if (!response.ok) {
    const body = await response.json().catch(() => null)
    throw new ApiError(body?.error?.message || body?.message || `Download failed (${response.status})`, response.status, body?.error?.code || body?.code)
  }
  const objectURL = URL.createObjectURL(await response.blob())
  const link = document.createElement('a')
  link.href = objectURL
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(objectURL)
}

async function request<T>(path: string, options: ApiOptions, allowRefresh: boolean): Promise<T> {
  const url = new URL(`${API_BASE}${path}`, window.location.origin)
  Object.entries(options.query ?? {}).forEach(([key, value]) => {
    if (value !== undefined && value !== '') url.searchParams.set(key, String(value))
  })

  const csrf = csrfToken()
  const response = await fetch(url, {
    ...options,
    credentials: 'include',
    headers: {
      Accept: 'application/json',
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...(csrf && options.method && !['GET', 'HEAD'].includes(options.method.toUpperCase()) ? { 'X-CSRF-Token': csrf } : {}),
      ...options.headers,
    },
  })

  const body = response.status === 204 ? null : await response.json().catch(() => null)
  if (!response.ok) {
    const error = body?.error
    if (response.status === 401 && allowRefresh && !path.includes('/auth/')) {
      const refresh = await fetch(`${API_BASE}/auth/refresh`, {
        method: 'POST', credentials: 'include', headers: csrf ? { 'X-CSRF-Token': csrf } : {},
      })
      if (refresh.ok) return request<T>(path, options, false)
    }
    if (response.status === 401 && !path.includes('/auth/login') && window.location.pathname !== '/login') window.location.assign('/login')
    throw new ApiError(error?.message || body?.message || `Request failed (${response.status})`, response.status, error?.code || body?.code)
  }
  return (body?.data ?? body) as T
}

function csrfToken() {
  const value = document.cookie.split('; ').find((part) => part.startsWith(CSRF_COOKIE))?.split('=').slice(1).join('=')
  return value ? decodeURIComponent(value) : undefined
}

export type PageResult<T> = { items: T[]; total: number; page?: number; page_size?: number; next_cursor?: string }

export function asPage<T>(value: unknown): PageResult<T> {
  if (Array.isArray(value)) return { items: value as T[], total: value.length }
  const result = (value || {}) as Record<string, unknown>
  const items = (result.items || result.results || result.data || []) as T[]
  return {
    items: Array.isArray(items) ? items : [],
    total: Number(result.total ?? (Array.isArray(items) ? items.length : 0)),
    page: Number(result.page || 1),
    page_size: Number(result.page_size || result.limit || 20),
    next_cursor: typeof result.next_cursor === 'string' ? result.next_cursor : undefined,
  }
}

export function formatNumber(value: unknown, compact = true) {
  if (value === null || value === undefined || value === '') return '—'
  const number = Number(value)
  if (!Number.isFinite(number)) return String(value)
  return new Intl.NumberFormat(currentLocale(), compact ? { notation: 'compact', maximumFractionDigits: 1 } : {}).format(number)
}

export function formatMoney(value: unknown) {
  if (value === null || value === undefined || value === '') return '—'
  return new Intl.NumberFormat(currentLocale(), { style: 'currency', currency: 'USD', maximumFractionDigits: 2 }).format(Number(value))
}

export function formatDate(value: unknown) {
  if (!value) return 'Never'
  const date = new Date(String(value))
  return Number.isNaN(date.valueOf()) ? String(value) : new Intl.DateTimeFormat(currentLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}
