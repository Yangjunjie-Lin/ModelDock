import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, asPage } from './api'

export type Organization = {
  id: string
  name: string
  slug: string
  status: string
  billing_region?: string
  created_at?: string
  updated_at?: string
}

export type OrganizationMember = {
  organization_id?: string
  user_id: string
  email?: string
  display_name?: string
  role: string
  status: string
  created_at?: string
}

export type Project = {
  id: string
  organization_id: string
  name: string
  slug: string
  description?: string
  status: string
  created_at?: string
  updated_at?: string
}

export type ProjectMember = {
  project_id?: string
  user_id: string
  email?: string
  display_name?: string
  role: string
  status: string
  created_at?: string
}

export type ProjectRouteGrant = {
  id?: string
  project_id?: string
  model_route_id: string
  alias?: string
  enabled: boolean
  routing_config?: Record<string, unknown>
  upstream_model?: string
  credential_group_id?: string
  created_at?: string
}

export type BudgetPolicy = {
  id: string
  project_id?: string
  name: string
  period: string
  token_limit?: number
  cost_limit?: string
  alert_threshold: string
  enforce_hard_limit: boolean
  status: string
  created_at?: string
}

export type BudgetUsage = {
  period?: string
  from?: string
  to?: string
  requests?: number
  input_tokens?: number
  cached_input_tokens?: number
  output_tokens?: number
  total_tokens?: number
  cost?: string
  errors?: number
}

export type BudgetEvent = {
  id: string
  event_type: string
  tokens?: number
  cost?: string
  request_id?: string
  created_at?: string
}

export type WebhookEndpoint = {
  id: string
  project_id?: string
  name: string
  url: string
  secret_last4?: string
  event_types: string[]
  enabled: boolean
  last_delivery_at?: string
  created_at?: string
}

export type WebhookDelivery = {
  id: string
  endpoint_id: string
  event_id?: string
  event_type: string
  status: string
  attempts: number
  max_attempts: number
  last_http_status?: number
  last_error?: string
  available_at?: string
  delivered_at?: string
  created_at?: string
}

export const v2Paths = {
  organizations: '/organizations',
  organization: (organizationID: string) => `/organizations/${organizationID}`,
  organizationMembers: (organizationID: string) => `/organizations/${organizationID}/members`,
  organizationMember: (organizationID: string, userID: string) => `/organizations/${organizationID}/members/${userID}`,
  organizationProjects: (organizationID: string) => `/organizations/${organizationID}/projects`,
  project: (projectID: string) => `/projects/${projectID}`,
  projectMembers: (projectID: string) => `/projects/${projectID}/members`,
  projectMember: (projectID: string, userID: string) => `/projects/${projectID}/members/${userID}`,
  projectRoutes: (projectID: string) => `/projects/${projectID}/routes`,
  projectRoute: (projectID: string, routeID: string) => `/projects/${projectID}/routes/${routeID}`,
  projectBudgets: (projectID: string) => `/projects/${projectID}/budgets`,
  projectBudget: (projectID: string, policyID: string) => `/projects/${projectID}/budgets/${policyID}`,
  projectBudgetUsage: (projectID: string) => `/projects/${projectID}/budget-usage`,
  projectBudgetEvents: (projectID: string) => `/projects/${projectID}/budget-events`,
  projectWebhooks: (projectID: string) => `/projects/${projectID}/webhooks`,
  projectWebhook: (projectID: string, webhookID: string) => `/projects/${projectID}/webhooks/${webhookID}`,
  projectWebhookTest: (projectID: string, webhookID: string) => `/projects/${projectID}/webhooks/${webhookID}/test`,
  projectWebhookDeliveries: (projectID: string) => `/projects/${projectID}/webhook-deliveries`,
  projectWebhookRetry: (projectID: string, deliveryID: string) => `/projects/${projectID}/webhook-deliveries/${deliveryID}/retry`,
  projectUsageExport: (projectID: string) => `/projects/${projectID}/usage/export`,
  projectAPIKeys: (_projectID?: string) => '/api-keys',
  projectAPIKey: (_projectID: string, keyID: string) => `/api-keys/${keyID}`,
  apiKeyRotate: (_projectID: string, keyID: string) => `/api-keys/${keyID}/rotate`,
  apiKeyFinalize: (_projectID: string, keyID: string) => `/api-keys/${keyID}/finalize`,
  alertAcknowledge: (alertID: string) => `/alerts/${alertID}/acknowledge`,
} as const

export function useAdminTenantScope() {
  const [organizationID, setOrganizationIDState] = useState(() => localStorage.getItem('rd-admin-organization') || '')
  const [projectID, setProjectIDState] = useState(() => localStorage.getItem('rd-admin-project') || '')
  const organizations = useQuery({
    queryKey: ['v2-organizations'],
    queryFn: () => api<unknown>(v2Paths.organizations, { query: { limit: 200 } }).then(asPage<Organization>),
  })
  const organizationRows = useMemo(() => organizations.data?.items || [], [organizations.data])

  useEffect(() => {
    if (!organizations.isSuccess) return
    if (organizationRows.length === 0) {
      setOrganizationIDState('')
      localStorage.removeItem('rd-admin-organization')
      return
    }
    if (!organizationRows.some((row) => row.id === organizationID)) setOrganizationIDState(organizationRows[0].id)
  }, [organizationID, organizationRows, organizations.isSuccess])

  const projects = useQuery({
    queryKey: ['v2-projects', organizationID],
    queryFn: () => api<unknown>(v2Paths.organizationProjects(organizationID), { query: { limit: 200 } }).then(asPage<Project>),
    enabled: Boolean(organizationID),
  })
  const projectRows = useMemo(() => projects.data?.items || [], [projects.data])

  useEffect(() => {
    if (projectRows.length === 0) {
      setProjectIDState('')
      return
    }
    if (!projectRows.some((row) => row.id === projectID)) setProjectIDState(projectRows[0].id)
  }, [projectID, projectRows])

  const setOrganizationID = (value: string) => {
    setOrganizationIDState(value)
    localStorage.setItem('rd-admin-organization', value)
    setProjectIDState('')
    localStorage.removeItem('rd-admin-project')
  }
  const setProjectID = (value: string) => {
    setProjectIDState(value)
    if (value) localStorage.setItem('rd-admin-project', value)
    else localStorage.removeItem('rd-admin-project')
  }

  return {
    organizationID,
    setOrganizationID,
    organizations,
    organizationRows,
    organization: organizationRows.find((row) => row.id === organizationID),
    projectID,
    setProjectID,
    projects,
    projectRows,
    project: projectRows.find((row) => row.id === projectID),
  }
}
