import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, asPage } from './api'

export type ConsoleOrganization = { id: string; name: string; slug: string; status: string }
export type ConsoleProject = { id: string; organization_id: string; organization_name?: string; organization_slug?: string; name: string; slug: string; description?: string; status: string }

type ProjectScope = {
  organizations: ConsoleOrganization[]
  organization?: ConsoleOrganization
  organizationID: string
  setOrganizationID: (value: string) => void
  projects: ConsoleProject[]
  project?: ConsoleProject
  projectID: string
  setProjectID: (value: string) => void
  loading: boolean
  error: unknown
  refresh: () => void
}

const ScopeContext = createContext<ProjectScope | null>(null)
export function ProjectScopeProvider({ children }: { children: ReactNode }) {
  const [organizationID, setOrganizationIDState] = useState(() => localStorage.getItem('rd-console-organization') || '')
  const [projectID, setProjectIDState] = useState(() => localStorage.getItem('rd-console-project') || '')
  const organizationQuery = useQuery({
    queryKey: ['console-organizations'],
    queryFn: () => api<unknown>('/organizations', { query: { limit: 200 } }).then(asPage<ConsoleOrganization>),
  })
  const organizations = useMemo(() => (organizationQuery.data?.items || []).filter((organization) => organization.status === 'ACTIVE'), [organizationQuery.data])

  useEffect(() => {
    if (!organizationQuery.isSuccess) return
    if (!organizations.length) {
      setOrganizationIDState('')
      return
    }
    if (!organizations.some((item) => item.id === organizationID)) setOrganizationIDState(organizations[0].id)
  }, [organizationID, organizationQuery.isSuccess, organizations])

  const projectQuery = useQuery({
    queryKey: ['console-projects', organizationID],
    queryFn: () => api<unknown>(`/organizations/${organizationID}/projects`, { query: { limit: 200 } }).then(asPage<ConsoleProject>),
    enabled: Boolean(organizationID),
  })
  const projects = useMemo(() => (projectQuery.data?.items || []).filter((project) => project.status === 'ACTIVE'), [projectQuery.data])

  useEffect(() => {
    if (!projects.length) {
      setProjectIDState('')
      return
    }
    if (!projects.some((item) => item.id === projectID)) setProjectIDState(projects[0].id)
  }, [projectID, projects])

  useEffect(() => {
    if (organizationID) localStorage.setItem('rd-console-organization', organizationID)
    else localStorage.removeItem('rd-console-organization')
  }, [organizationID])
  useEffect(() => {
    if (projectID) localStorage.setItem('rd-console-project', projectID)
    else localStorage.removeItem('rd-console-project')
  }, [projectID])

  const setOrganizationID = (value: string) => {
    setOrganizationIDState(value)
    setProjectIDState('')
  }
  const value: ProjectScope = {
    organizations,
    organization: organizations.find((item) => item.id === organizationID),
    organizationID,
    setOrganizationID,
    projects,
    project: projects.find((item) => item.id === projectID),
    projectID,
    setProjectID: setProjectIDState,
    loading: organizationQuery.isLoading || projectQuery.isLoading,
    error: organizationQuery.error || projectQuery.error,
    refresh: () => { void organizationQuery.refetch(); if (organizationID) void projectQuery.refetch() },
  }
  return <ScopeContext.Provider value={value}>{children}</ScopeContext.Provider>
}

export function useProjectScope() {
  const value = useContext(ScopeContext)
  if (!value) throw new Error('useProjectScope must be used inside ProjectScopeProvider')
  return value
}

export const consoleV2Paths = {
  projectModels: (_projectID?: string) => '/models',
  projectAPIKeys: (_projectID?: string) => '/api-keys',
  projectAPIKey: (_projectID: string, keyID: string) => `/api-keys/${keyID}`,
  projectUsage: (_projectID?: string) => '/usage',
  projectLogs: (_projectID?: string) => '/request-logs',
  projectUsageExport: (projectID: string) => `/projects/${projectID}/usage/export`,
  apiKeyRotate: (_projectID: string, keyID: string) => `/api-keys/${keyID}/rotate`,
  apiKeyFinalize: (_projectID: string, keyID: string) => `/api-keys/${keyID}/finalize`,
} as const
