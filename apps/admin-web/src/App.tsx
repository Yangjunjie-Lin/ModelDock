import { Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from './components/AppShell'
import { CredentialsPage } from './pages/CredentialsPage'
import { DashboardPage } from './pages/DashboardPage'
import { BillingPage } from './pages/BillingPage'
import { LoginPage } from './pages/LoginPage'
import { OrganizationsPage } from './pages/OrganizationsPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { AdminApiKeysPage, AlertsPage, AuditLogsPage, GroupsPage, MarketplacePage, ModelsPage, NotFoundPage, ProvidersPage, RequestLogsPage, RoutesPage, RoutingRulesPage, TeamsPage, UsagePage, UsersPage } from './pages/ResourcePages'
import { SettingsPage } from './pages/SettingsPage'
import { WebhooksPage } from './pages/WebhooksPage'

export function App() {
  return <Routes>
    <Route path="/login" element={<LoginPage />} />
    <Route element={<AppShell />}>
      <Route index element={<DashboardPage />} />
      <Route path="organizations" element={<OrganizationsPage />} />
      <Route path="projects" element={<ProjectsPage />} />
      <Route path="teams" element={<TeamsPage />} />
      <Route path="providers" element={<ProvidersPage />} />
      <Route path="marketplace" element={<MarketplacePage />} />
      <Route path="credentials" element={<CredentialsPage />} />
      <Route path="groups" element={<GroupsPage />} />
      <Route path="models" element={<ModelsPage />} />
      <Route path="routes" element={<RoutesPage />} />
      <Route path="routing-rules" element={<RoutingRulesPage />} />
      <Route path="billing" element={<BillingPage />} />
      <Route path="api-keys" element={<AdminApiKeysPage />} />
      <Route path="users" element={<UsersPage />} />
      <Route path="usage" element={<UsagePage />} />
      <Route path="request-logs" element={<RequestLogsPage />} />
      <Route path="audit-logs" element={<AuditLogsPage />} />
      <Route path="alerts" element={<AlertsPage />} />
      <Route path="webhooks" element={<WebhooksPage />} />
      <Route path="settings" element={<SettingsPage />} />
      <Route path="admin/*" element={<Navigate to="/" replace />} />
      <Route path="*" element={<NotFoundPage />} />
    </Route>
  </Routes>
}
