import { currentLocale } from './i18n'

const API_BASE = ((import.meta.env.VITE_API_BASE as string | undefined) || '/api/console').replace(/\/$/, '')
const CSRF_COOKIE = 'relayedock_console_csrf='

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

/**
 * Formats an exact decimal amount without crossing a binary floating-point
 * boundary. Finance APIs return amounts as strings; keeping grouping and
 * fractional handling string-only prevents rounding of NUMERIC values.
 */
export function formatMoney(value: unknown, currency = 'USD') {
  if (value === null || value === undefined || value === '') return '—'
  const raw = typeof value === 'string' ? value.trim() : String(value)
  const match = raw.match(/^([+-]?)(\d+)(?:\.(\d+))?$/)
  const code = currency.trim().toUpperCase() || 'USD'
  if (!match) return `${code} ${raw}`

  const sign = match[1]
  const integer = match[2].replace(/^0+(?=\d)/, '')
  const grouped = integer.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const sourceFraction = match[3] || ''
  const significantFraction = sourceFraction.replace(/0+$/, '')
  const fraction = significantFraction.length >= 2 ? significantFraction : significantFraction.padEnd(2, '0') || '00'
  const visibleSign = /^0+$/.test(`${match[2]}${sourceFraction}`) ? '' : sign
  return `${code} ${visibleSign}${grouped}.${fraction}`
}

/** Adds signed decimal strings exactly; no monetary value is converted to Number. */
export function addDecimalStrings(values: unknown[]) {
  const parsed = values.map((value) => String(value ?? '0').trim()).map((value) => {
    const match = value.match(/^([+-]?)(\d+)(?:\.(\d+))?$/)
    if (!match) return null
    return { negative: match[1] === '-', integer: match[2], fraction: match[3] || '' }
  })
  if (parsed.some((value) => !value)) return '—'
  const scale = parsed.reduce((maximum, value) => Math.max(maximum, value?.fraction.length || 0), 0)
  const total = parsed.reduce((sum, value) => {
    if (!value) return sum
    const magnitude = BigInt(`${value.integer}${value.fraction.padEnd(scale, '0')}`)
    return sum + (value.negative ? -magnitude : magnitude)
  }, 0n)
  const negative = total < 0n
  const digits = (negative ? -total : total).toString().padStart(scale + 1, '0')
  const integer = scale ? digits.slice(0, -scale) : digits
  const fraction = scale ? digits.slice(-scale).replace(/0+$/, '') : ''
  return `${negative ? '-' : ''}${integer}${fraction ? `.${fraction}` : ''}`
}

/**
 * Computes an exact BigInt ratio and converts only the final percentage used
 * for a CSS progress bar. This result is display-only and never posted back.
 */
export function decimalRatioPercent(value: unknown, limit: unknown): number | null {
  const parse = (input: unknown) => {
    const match = String(input ?? '0').trim().match(/^(\d+)(?:\.(\d+))?$/)
    return match ? { integer: match[1], fraction: match[2] || '' } : null
  }
  const left = parse(value), right = parse(limit)
  if (!left || !right) return null
  const scale = Math.max(left.fraction.length, right.fraction.length)
  const numerator = BigInt(`${left.integer}${left.fraction.padEnd(scale, '0')}`)
  const denominator = BigInt(`${right.integer}${right.fraction.padEnd(scale, '0')}`)
  if (denominator <= 0n) return null
  const tenths = numerator * 1000n / denominator
  return Math.min(100, Number(tenths) / 10)
}

export function formatDate(value: unknown) {
  if (!value) return 'Never'
  const date = new Date(String(value))
  return Number.isNaN(date.valueOf()) ? String(value) : new Intl.DateTimeFormat(currentLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}
