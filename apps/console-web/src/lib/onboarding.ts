export type OnboardingStep = {
  key: string
  completed: boolean
  completed_at?: string
  resource_id?: string
  required: boolean
}

export type OnboardingState = {
  steps: OnboardingStep[]
  next_step: string
  complete: boolean
  organization_id?: string
  project_id?: string
}

export type OnboardingFacts = {
  registered: boolean
  email_verified: boolean
  organization_created: boolean
  plan_selected: boolean
  first_recharge: boolean
  api_key_created: boolean
  first_api_call: boolean
  usage_visible: boolean
}

export function deriveOnboardingFacts(steps: OnboardingStep[]): OnboardingFacts {
  const done = (key: string) => steps.find((step) => step.key === key)?.completed === true
  return {
    registered: done('REGISTER'),
    email_verified: done('VERIFY_EMAIL'),
    organization_created: done('CREATE_ORGANIZATION'),
    plan_selected: done('SELECT_PLAN'),
    first_recharge: done('RECHARGE'),
    api_key_created: done('CREATE_API_KEY'),
    first_api_call: done('FIRST_API_CALL'),
    usage_visible: done('VIEW_USAGE_AND_CHARGE'),
  }
}
