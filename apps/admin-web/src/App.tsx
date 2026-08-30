import { Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from './components/AppShell'
import { CredentialsPage } from './pages/CredentialsPage'
import { DashboardPage } from './pages/DashboardPage'
import { BillingPage } from './pages/BillingPage'
import { LoginPage } from './pages/LoginPage'
import { OrganizationsPage } from './pages/OrganizationsPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { AdminApiKeysPage, AlertsPage, AuditLogsPage, GroupsPage, ModelsPage, NotFoundPage, ProvidersPage, RequestLogsPage, RoutesPage, RoutingRulesPage, TeamsPage, UsagePage, UsersPage } from './pages/ResourcePages'
import { SettingsPage } from './pages/SettingsPage'
import { WebhooksPage } from './pages/WebhooksPage'
import { MFASetupPage } from './pages/MFASetupPage'
import { PricingPage } from './pages/PricingPage'
import { PaymentsPage } from './pages/PaymentsPage'
import { SubscriptionsPage } from './pages/SubscriptionsPage'
import { FinancePage } from './pages/FinancePage'
import { ReconciliationPage } from './pages/ReconciliationPage'
import { GovernancePage } from './pages/GovernancePage'
import { StatusPage, SupportPage } from './pages/OperationsPage'
import { AcquisitionPage } from './pages/AcquisitionPage'
import { SupplierApplicationsPage } from './pages/SupplierApplicationsPage'
import { ProviderQualityPage } from './pages/ProviderQualityPage'
import { SupplierSettlementsPage } from './pages/SupplierSettlementsPage'
import { MarketplaceLaunchPage } from './pages/MarketplaceLaunchPage'
import { ProviderAccountsPage } from './pages/ProviderAccountsPage'
import { ProviderCapabilitiesPage } from './pages/ProviderCapabilitiesPage'

export function App() {
  return <Routes>
    <Route path="/login" element={<LoginPage />} />
    <Route path="/mfa-setup" element={<MFASetupPage />} />
    <Route element={<AppShell />}>
      <Route index element={<DashboardPage />} />
      <Route path="organizations" element={<OrganizationsPage />} />
      <Route path="projects" element={<ProjectsPage />} />
      <Route path="teams" element={<TeamsPage />} />
      <Route path="providers" element={<ProvidersPage />} />
      <Route path="marketplace" element={<MarketplaceLaunchPage />} />
      <Route path="credentials" element={<CredentialsPage />} />
      <Route path="provider-accounts" element={<ProviderAccountsPage />} />
      <Route path="provider-capabilities" element={<ProviderCapabilitiesPage />} />
      <Route path="groups" element={<GroupsPage />} />
      <Route path="models" element={<ModelsPage />} />
      <Route path="routes" element={<RoutesPage />} />
      <Route path="routing-rules" element={<RoutingRulesPage />} />
      <Route path="billing" element={<BillingPage />} />
      <Route path="pricing" element={<PricingPage />} />
      <Route path="payments" element={<PaymentsPage />} />
      <Route path="subscriptions" element={<SubscriptionsPage />} />
      <Route path="finance" element={<FinancePage />} />
      <Route path="reconciliation" element={<ReconciliationPage />} />
      <Route path="governance" element={<GovernancePage />} />
      <Route path="api-keys" element={<AdminApiKeysPage />} />
      <Route path="users" element={<UsersPage />} />
      <Route path="usage" element={<UsagePage />} />
      <Route path="request-logs" element={<RequestLogsPage />} />
      <Route path="audit-logs" element={<AuditLogsPage />} />
      <Route path="alerts" element={<AlertsPage />} />
      <Route path="status" element={<StatusPage />} />
      <Route path="support" element={<SupportPage />} />
      <Route path="acquisition" element={<AcquisitionPage />} />
      <Route path="suppliers" element={<SupplierApplicationsPage />} />
      <Route path="provider-quality" element={<ProviderQualityPage />} />
      <Route path="supplier-settlements" element={<SupplierSettlementsPage />} />
      <Route path="webhooks" element={<WebhooksPage />} />
      <Route path="settings" element={<SettingsPage />} />
      <Route path="admin/*" element={<Navigate to="/" replace />} />
      <Route path="*" element={<NotFoundPage />} />
    </Route>
  </Routes>
}
