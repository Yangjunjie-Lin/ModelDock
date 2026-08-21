import { describe, expect, it } from 'vitest'
import { deriveOnboardingFacts, type OnboardingStep } from './onboarding'
import { approvedPaymentChannelFee, commercialTermsApproved, deliverablePublicEmail, findPurchasablePublicModel, isPublicModelAvailable, isPublicProviderAvailable, paymentProviderRuntimeAdmitted, publicModelAllowedRegions, publicTokenPriceName, type PublicModel, type PublicModelPrice, type PublicPricing, type PublicProvider } from './public-api'

describe('public commercial contract adapters', () => {
  it('uses nested model availability from the final public API contract', () => {
    const model = {
      id: 'model-id', provider_id: 'provider-id', provider_slug: 'provider', provider_name: 'Provider',
      provider_model_id: 'upstream-model', display_name: 'Published model', model_type: 'text', capabilities: [],
      allowed_regions: ['CN'], generated_content_label_capability: 'UNKNOWN',
      availability: { available: true, region: 'CN', status: 'AVAILABLE' }, pricing: null, updated_at: '2026-08-16T00:00:00Z',
    } satisfies PublicModel
    expect(isPublicModelAvailable(model)).toBe(true)
    expect(publicModelAllowedRegions(model)).toEqual(['CN'])
  })

  it('does not infer Provider availability when the public evidence is unavailable', () => {
    const provider = {
      id: 'provider-id', slug: 'provider', name: 'Provider', provider_type: 'openai', commercial_status: 'ACTIVE',
      commercial_resale_status: 'AUTHORIZED', enabled: true, allowed_customer_regions: ['CN'], prohibited_regions: [],
      data_processing_regions: ['CN'], pricing_disabled: false, emergency_kill_switch: false, technical_status: 'UNKNOWN',
      enabled_model_count: 1, availability: { available: false, status: 'UNAVAILABLE', reason_code: 'REGION_NOT_ALLOWED', region: 'CN' },
    } satisfies PublicProvider
    expect(isPublicProviderAvailable(provider)).toBe(false)
  })

  it('accepts only approved PAYMENT_CHANNEL evidence for the selected provider', () => {
    const pricing = {
      region: 'CN', currency: 'CNY', subscription_plans: [], token_prices: [], terms_configured: true,
      payment_fees_configured: true, payment_region_supported: true, updated_at: '2026-08-16T00:00:00Z',
      commercial_terms: {
        id: 'terms-id', region: 'CN', currency: 'CNY', subscription_tax_included: null, token_tax_included: null,
        tax_disclosure: 'Tax treatment is disclosed at checkout.', refund_summary: 'Eligibility review applies.', refund_policy_url: '/legal/refunds',
        bonus_credit_amount: '0', bonus_non_refundable: true, legal_review_required: true, legal_review_status: 'APPROVED',
        effective_at: '2026-08-16T00:00:00Z', created_at: '2026-08-16T00:00:00Z',
      },
      payment_fees: [
        { id: 'platform', fee_category: 'PLATFORM_SERVICE', payment_provider: 'sandbox', region: 'CN', currency: 'CNY', fee_kind: 'NONE', fixed_amount: '0', rate_bps: 0, charged_to_customer: false, description: 'Platform evidence.', legal_review_required: true, legal_review_status: 'APPROVED', effective_at: '2026-08-16T00:00:00Z', created_at: '2026-08-16T00:00:00Z' },
        { id: 'pending-channel', fee_category: 'PAYMENT_CHANNEL', payment_provider: 'sandbox', region: 'CN', currency: 'CNY', fee_kind: 'NONE', fixed_amount: '0', rate_bps: 0, charged_to_customer: false, description: 'Pending channel evidence.', legal_review_required: true, legal_review_status: 'PENDING', effective_at: '2026-08-16T00:00:00Z', created_at: '2026-08-16T00:00:00Z' },
        { id: 'approved-channel', fee_category: 'PAYMENT_CHANNEL', payment_provider: 'manual_transfer', region: 'CN', currency: 'CNY', fee_kind: 'NONE', fixed_amount: '0', rate_bps: 0, charged_to_customer: false, description: 'Explicit zero channel fee.', legal_review_required: true, legal_review_status: 'APPROVED', effective_at: '2026-08-16T00:00:00Z', created_at: '2026-08-16T00:00:00Z' },
      ],
    } satisfies PublicPricing
    expect(commercialTermsApproved(pricing.commercial_terms)).toBe(true)
    expect(approvedPaymentChannelFee(pricing, 'sandbox')).toBeUndefined()
    expect(approvedPaymentChannelFee(pricing, 'manual_transfer')?.id).toBe('approved-channel')
  })

  it('uses the OpenAPI model_name field for public token prices', () => {
    const price = {
      price_book_id: 'price-id', provider_id: 'provider-id', provider_name: 'Provider', provider_slug: 'provider',
      model_id: 'model-id', model_name: 'Published model', provider_model_id: 'upstream-model',
      input_token_price: '0.001', cached_input_token_price: '0.0005', output_token_price: '0.002', request_fixed_price: '0',
      currency: 'CNY', unit: 1000, effective_at: '2026-08-16T00:00:00Z', availability: { available: true, region: 'CN', status: 'AVAILABLE' },
    } satisfies PublicModelPrice
    expect(publicTokenPriceName(price)).toBe('Published model')
  })

  it('does not turn the non-delivering deployment placeholder into a mail link', () => {
    expect(deliverablePublicEmail('support@example.invalid')).toBeUndefined()
    expect(deliverablePublicEmail(' support@example.com ')).toBe('support@example.com')
  })

  it('joins a project alias only to the exact available Provider/model price evidence', () => {
    const model = {
      id: 'model-id', provider_id: 'provider-id', provider_slug: 'provider', provider_name: 'Provider',
      provider_model_id: 'upstream-model', display_name: 'Published model', model_type: 'text', capabilities: [],
      allowed_regions: ['CN'], generated_content_label_capability: 'UNKNOWN',
      availability: { available: true, region: 'CN', status: 'AVAILABLE' },
      pricing: { price_book_id: 'price-id', provider_id: 'provider-id', provider_name: 'Provider', provider_slug: 'provider', model_id: 'model-id', model_name: 'Published model', provider_model_id: 'upstream-model', input_token_price: '1', cached_input_token_price: '0.5', output_token_price: '2', request_fixed_price: '0', currency: 'CNY', unit: 1000000, availability: { available: true, region: 'CN', status: 'AVAILABLE' } },
      updated_at: '2026-08-16T00:00:00Z',
    } satisfies PublicModel
    expect(findPurchasablePublicModel({ provider_id: 'provider-id', upstream_model: 'upstream-model' }, [model])).toBe(model)
    expect(findPurchasablePublicModel({ provider_id: 'other-provider', upstream_model: 'upstream-model' }, [model])).toBeUndefined()
  })

  it('uses the same payment contract and region gates as the server registry', () => {
    expect(paymentProviderRuntimeAdmitted({ contract_status: 'TEST_ONLY', production_ready: false, allowed_regions: ['CN'] }, 'CN')).toBe(true)
    expect(paymentProviderRuntimeAdmitted({ contract_status: 'ACTIVE', production_ready: false, allowed_regions: ['CN'] }, 'CN')).toBe(false)
    expect(paymentProviderRuntimeAdmitted({ contract_status: 'ACTIVE', production_ready: true, allowed_regions: ['CN'] }, 'US')).toBe(false)
  })
})

describe('onboarding facts', () => {
  it('derives only server-completed milestones from the array contract', () => {
    const step = (key: string, completed: boolean): OnboardingStep => ({ key, completed, required: true })
    const facts = deriveOnboardingFacts([
      step('REGISTER', true), step('VERIFY_EMAIL', true), step('CREATE_ORGANIZATION', true), step('SELECT_PLAN', false),
      step('RECHARGE', false), step('CREATE_API_KEY', false), step('FIRST_API_CALL', false), step('VIEW_USAGE_AND_CHARGE', false),
    ])
    expect(facts).toEqual({ registered: true, email_verified: true, organization_created: true, plan_selected: false, first_recharge: false, api_key_created: false, first_api_call: false, usage_visible: false })
  })
})
