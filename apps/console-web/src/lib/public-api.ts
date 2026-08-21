import { ApiError } from './api'

const PUBLIC_API_BASE = ((import.meta.env.VITE_PUBLIC_API_BASE as string | undefined) || '/api/public').replace(/\/$/, '')

type PublicApiOptions = RequestInit & { query?: Record<string, string | number | boolean | undefined> }

export async function publicApi<T>(path: string, options: PublicApiOptions = {}): Promise<T> {
  const url = new URL(`${PUBLIC_API_BASE}${path}`, window.location.origin)
  Object.entries(options.query || {}).forEach(([key, value]) => {
    if (value !== undefined && value !== '') url.searchParams.set(key, String(value))
  })
  const response = await fetch(url, {
    ...options,
    headers: {
      Accept: 'application/json',
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...options.headers,
    },
  })
  const body = response.status === 204 ? null : await response.json().catch(() => null)
  if (!response.ok) {
    const error = body?.error
    throw new ApiError(error?.message || body?.message || `Public API request failed (${response.status})`, response.status, error?.code || body?.code)
  }
  return (body?.data ?? body) as T
}

export type PublicConfig = {
  product: string
  compatibility_name: string
  registration_mode: 'CLOSED' | 'INVITE_ONLY' | 'PUBLIC'
  email_verification_required: boolean
  support_email: string
  enterprise_email: string
  legal_review_required: boolean
  supported_regions?: string[]
  supported_currencies?: string[]
  supported_funnel_events?: string[]
}

export type PublicModelPrice = {
  price_book_id: string
  model_id: string
  model_name: string
  provider_model_id: string
  provider_id: string
  provider_name: string
  provider_slug: string
  input_token_price: string
  cached_input_token_price: string
  output_token_price: string
  request_fixed_price: string
  currency: string
  unit: string | number
  effective_at?: string
  expires_at?: string | null
  availability: {
    available: boolean
    region: string
    status: string
    reason_code?: string
  }
}

export type PublicModel = {
  id: string
  provider_id: string
  provider_slug: string
  provider_name: string
  provider_model_id: string
  display_name: string
  model_type: string
  capabilities: string[]
  context_window?: number | null
  allowed_regions: string[]
  service_subject?: string
  filing_info?: string
  generated_content_label_capability: string
  user_disclosure?: string
  availability: {
    available: boolean
    region: string
    status: string
    reason_code?: string
  }
  pricing: PublicModelPrice | null
  updated_at: string
}

export type PublicModelCatalog = {
  items: PublicModel[]
  region: string
  currency: string
  updated_at?: string
}

export type PublicProvider = {
  id: string
  slug: string
  name: string
  provider_type: string
  commercial_status: string
  resale_status?: string
  commercial_resale_status: string
  enabled: boolean
  kill_switch?: boolean
  available?: boolean
  availability: { available: boolean; status: string; reason_code?: string; region: string }
  availability_reason?: string
  allowed_regions?: string[]
  allowed_customer_regions: string[]
  prohibited_regions: string[]
  data_processing_regions: string[]
  data_retention_policy?: string
  terms_version?: string
  available_model_count?: number
  enabled_model_count: number
  pricing_disabled: boolean
  emergency_kill_switch: boolean
  technical_status: 'UNKNOWN' | 'DISABLED' | 'OPERATIONAL'
  published_uptime?: string
  declared_uptime?: string
  quality_grade?: 'UNKNOWN' | 'A' | 'B' | 'C' | 'D' | 'F'
  quality_source?: 'PLATFORM_MEASURED'
  quality_measurement_count?: number
}

export type PublicProviderCatalog = {
  items: PublicProvider[]
  region: string
  updated_at?: string
}

export type PublicSubscriptionPlan = {
  plan_id: string
  slug: string
  name: string
  description?: string
  plan_version_id: string
  version?: number
  subscription_fee: string
  currency: string
  billing_interval: string
  trial_days: number
  token_billing_mode: string
  enterprise_contract?: boolean
  contact_sales?: boolean
}

export type PublicPaymentFee = {
  id: string
  provider?: string
  payment_provider: string
  name?: string
  fee_category: 'PAYMENT_CHANNEL' | 'PLATFORM_SERVICE'
  fee_type?: string
  fee_kind: 'NONE' | 'FIXED' | 'PERCENT' | 'FIXED_PLUS_PERCENT'
  fixed_amount: string
  fixed_fee?: string
  percentage_rate?: string
  rate_bps: number
  percentage_bps?: number
  charged_to_customer: boolean
  currency: string
  region: string
  description: string
  legal_review_required: boolean
  legal_review_status: 'PENDING' | 'APPROVED'
  effective_at: string
  expires_at?: string | null
  created_at: string
}

export type PublicCommercialTerms = {
  id: string
  region: string
  currency: string
  bonus_credit_amount: string
  bonus_non_refundable: boolean
  tax_included?: boolean | null
  subscription_tax_included: boolean | null
  token_tax_included: boolean | null
  tax_rate?: string
  tax_disclosure: string
  tax_description?: string
  refund_summary: string
  refund_policy_url: string
  legal_review_status: 'PENDING' | 'APPROVED'
  legal_review_required: boolean
  effective_at: string
  expires_at?: string | null
  created_at: string
}

export type PublicPricing = {
  region: string
  currency: string
  subscription_plans: PublicSubscriptionPlan[]
  token_prices: PublicModelPrice[]
  payment_fees: PublicPaymentFee[]
  commercial_terms: PublicCommercialTerms | null
  terms_configured: boolean
  payment_fees_configured: boolean
  payment_region_supported: boolean
  updated_at: string
}

export type PublicStatusComponent = { status: string; message?: string }
export type PublicStatus = {
  status: string
  updated_at: string
  components: {
    gateway: PublicStatusComponent
    dashboard: PublicStatusComponent
    billing: PublicStatusComponent
    providers: Array<PublicStatusComponent & { name: string }>
  }
  events: Array<{
    id: string
    summary: string
    component: string
    status: string
    public_message: string
    started_at: string
    resolved_at?: string
  }>
}

export function isPublicModelAvailable(model: PublicModel) {
  return model.availability.available
}

export function findPurchasablePublicModel(route: { provider_id?: string; upstream_model?: string }, models: PublicModel[]) {
  if (!route.provider_id || !route.upstream_model) return undefined
  return models.find((model) => model.provider_id === route.provider_id && model.provider_model_id === route.upstream_model && isPublicModelAvailable(model) && Boolean(model.pricing))
}

export function isPublicProviderAvailable(provider: PublicProvider) {
  return provider.available ?? provider.availability?.available ?? provider.availability?.status === 'AVAILABLE'
}

export function publicModelAllowedRegions(model: PublicModel) {
  return model.allowed_regions
}

export function commercialTermsApproved(terms?: PublicCommercialTerms | null) {
  return terms?.legal_review_status === 'APPROVED'
}

export function approvedPaymentChannelFee(pricing: PublicPricing | undefined, paymentProvider?: string) {
  if (!pricing) return undefined
  return pricing.payment_fees.find((fee) => fee.fee_category === 'PAYMENT_CHANNEL' &&
    fee.legal_review_status === 'APPROVED' && (!paymentProvider || fee.payment_provider === paymentProvider))
}

export function paymentProviderRuntimeAdmitted(provider: { enabled?: boolean; contract_status: string; production_ready: boolean; allowed_regions: string[] }, region: string) {
  if (provider.enabled === false) return false
  const contractReady = provider.contract_status === 'TEST_ONLY' || provider.contract_status === 'INTERNAL_APPROVED' ||
    (provider.contract_status === 'ACTIVE' && provider.production_ready)
  return contractReady && provider.allowed_regions.some((allowed) => allowed === '*' || allowed === region)
}

export function publicTokenPriceName(price: PublicModelPrice) {
  return price.model_name || price.provider_model_id || price.model_id
}

export function deliverablePublicEmail(value?: string) {
  const email = value?.trim()
  if (!email || email.toLowerCase().endsWith('.invalid')) return undefined
  return email
}
