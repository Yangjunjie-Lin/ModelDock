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

export async function apiDownload(path: string, filename: string, query: ApiOptions['query'] = {}, headers: HeadersInit = {}): Promise<void> {
  const url = new URL(`${API_BASE}${path}`, window.location.origin)
  Object.entries(query ?? {}).forEach(([key, value]) => {
    if (value !== undefined && value !== '') url.searchParams.set(key, String(value))
  })
  let response = await fetch(url, { credentials: 'include', headers: { Accept: 'text/csv', ...headers } })
  if (response.status === 401) {
    const csrf = csrfToken()
    const refresh = await fetch(`${API_BASE}/auth/refresh`, { method: 'POST', credentials: 'include', headers: csrf ? { 'X-CSRF-Token': csrf } : {} })
    if (refresh.ok) response = await fetch(url, { credentials: 'include', headers: { Accept: 'text/csv', ...headers } })
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
  const hasContentType = new Headers(options.headers).has('Content-Type')
  const response = await fetch(url, {
    ...options,
    credentials: 'include',
    headers: {
      Accept: 'application/json',
      ...(options.body && !hasContentType ? { 'Content-Type': 'application/json' } : {}),
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
 * Formats an API decimal without crossing a JavaScript binary floating-point
 * boundary. Finance endpoints intentionally return monetary values as strings.
 */
export function formatMoneyString(value: unknown, currency: unknown = 'USD') {
  if (value === null || value === undefined || value === '') return '—'
  const raw = String(value).trim()
  const code = String(currency || 'USD').trim().toUpperCase()
  const match = raw.match(/^([+-]?)(\d+)(?:\.(\d+))?$/)
  if (!match) return `${code} ${raw}`

  const [, sign, integerPart, sourceFraction = ''] = match
  const integer = integerPart.replace(/^0+(?=\d)/, '').replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const significantFraction = sourceFraction.replace(/0+$/, '')
  const fraction = significantFraction.length > 0
    ? significantFraction.padEnd(2, '0')
    : '00'
  const visibleSign = /^0+$/.test(`${integerPart}${sourceFraction}`) ? '' : sign
  return `${code} ${visibleSign}${integer}.${fraction}`
}

export function formatMoney(value: unknown, currency: unknown = 'USD') {
  return formatMoneyString(value, currency)
}

/** Adds signed decimal strings exactly, preserving the largest input scale. */
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

export function maximumDecimalString(values: unknown[]) {
  return values.reduce<string>((maximum, value) => {
    const candidate = String(value ?? '0').trim()
    return compareNonNegativeDecimals(candidate, maximum) > 0 ? candidate : maximum
  }, '0')
}

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

export function percentStringToRatio(value: string) {
  const match = value.trim().match(/^(\d{1,3})$/)
  if (!match) return '0'
  const percent = BigInt(match[1])
  const bounded = percent > 100n ? 100n : percent
  const digits = bounded.toString().padStart(3, '0')
  return `${digits.slice(0, -2)}.${digits.slice(-2)}`.replace(/\.?0+$/, '') || '0'
}

function compareNonNegativeDecimals(left: string, right: string) {
  const parse = (value: string) => {
    const match = value.match(/^(\d+)(?:\.(\d+))?$/)
    return match ? { integer: match[1].replace(/^0+(?=\d)/, ''), fraction: match[2] || '' } : { integer: '0', fraction: '' }
  }
  const a = parse(left), b = parse(right)
  if (a.integer.length !== b.integer.length) return a.integer.length > b.integer.length ? 1 : -1
  if (a.integer !== b.integer) return a.integer > b.integer ? 1 : -1
  const scale = Math.max(a.fraction.length, b.fraction.length)
  const af = a.fraction.padEnd(scale, '0'), bf = b.fraction.padEnd(scale, '0')
  return af === bf ? 0 : af > bf ? 1 : -1
}

export function formatDate(value: unknown) {
  if (!value) return 'Never'
  const date = new Date(String(value))
  return Number.isNaN(date.valueOf()) ? String(value) : new Intl.DateTimeFormat(currentLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}
