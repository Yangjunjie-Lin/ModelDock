import { Navigate, Route, Routes } from 'react-router-dom'
import { ConsoleShell } from './components/ConsoleShell'
import { PublicShell } from './components/PublicShell'
import { ApiKeysPage } from './pages/ApiKeysPage'
import { LogsPage, ModelsPage, NotFoundPage, UsagePage } from './pages/DataPages'
import { DeveloperDocsPage } from './pages/DeveloperDocsPage'
import { LoginPage } from './pages/LoginPage'
import { OverviewPage } from './pages/OverviewPage'
import { PlaygroundPage } from './pages/PlaygroundPage'
import { AccountPage } from './pages/AccountPage'
import { ForgotPasswordPage, InvitationPage, RegisterPage, ResetPasswordPage, VerifyEmailPage } from './pages/AuthPages'
import { ProjectScopeProvider } from './lib/project-scope'
import { PaymentsPage, PaymentSuccessPage } from './pages/PaymentsPage'
import { SubscriptionPage } from './pages/SubscriptionPage'
import { BillingPage } from './pages/BillingPage'
import { ByokPage } from './pages/ByokPage'
import { StatusPage, SupportPage } from './pages/OperationsPage'
import { OnboardingPage } from './pages/OnboardingPage'
import { LegalPage } from './pages/LegalPages'
import { ContactPage, EnterprisePage, HomePage, ModelsCatalogPage, PricingPage, ProductPage, ProvidersPage, PublicNotFoundPage, PublicStatusPage } from './pages/PublicPages'
import { PublicSettingsProvider } from './lib/public-settings'
import { SupplierOnboardingPage } from './pages/SupplierOnboardingPage'
import { SupplierSettlementsPage } from './pages/SupplierSettlementsPage'
import { ProviderAccountsPage } from './pages/ProviderAccountsPage'
import { ProviderHealthPage } from './pages/ProviderHealthPage'
import { WorkspacePage } from './pages/WorkspacePage'

export function App() {
  return <PublicSettingsProvider><Routes>
    <Route element={<PublicShell />}>
      <Route index element={<HomePage />} />
      <Route path="product" element={<ProductPage />} />
      <Route path="models" element={<ModelsCatalogPage />} />
      <Route path="providers" element={<ProvidersPage />} />
      <Route path="pricing" element={<PricingPage />} />
      <Route path="docs" element={<DeveloperDocsPage />} />
      <Route path="status" element={<PublicStatusPage />} />
      <Route path="contact" element={<ContactPage />} />
      <Route path="enterprise" element={<EnterprisePage />} />
      <Route path="legal/:document" element={<LegalPage />} />
      <Route path="*" element={<PublicNotFoundPage />} />
    </Route>
    <Route path="/login" element={<LoginPage />} />
    <Route path="/register" element={<RegisterPage />} />
    <Route path="/forgot-password" element={<ForgotPasswordPage />} />
    <Route path="/reset-password" element={<ResetPasswordPage />} />
    <Route path="/verify-email" element={<VerifyEmailPage />} />
    <Route path="/invitations/:token" element={<InvitationPage />} />
    <Route path="/console" element={<ProjectScopeProvider><ConsoleShell /></ProjectScopeProvider>}>
      <Route index element={<OverviewPage />} />
      <Route path="workspace" element={<WorkspacePage />} />
      <Route path="onboarding" element={<OnboardingPage />} />
      <Route path="supplier" element={<SupplierOnboardingPage />} />
      <Route path="supplier-settlements" element={<SupplierSettlementsPage />} />
      <Route path="api-keys" element={<ApiKeysPage />} />
      <Route path="byok" element={<ByokPage />} />
      <Route path="provider-accounts" element={<ProviderAccountsPage />} />
      <Route path="provider-health" element={<ProviderHealthPage />} />
      <Route path="models" element={<ModelsPage />} />
      <Route path="usage" element={<UsagePage />} />
      <Route path="logs" element={<LogsPage />} />
      <Route path="recharge" element={<PaymentsPage />} />
      <Route path="subscription" element={<SubscriptionPage />} />
      <Route path="billing" element={<BillingPage />} />
      <Route path="payments/success" element={<PaymentSuccessPage />} />
      <Route path="playground" element={<PlaygroundPage />} />
      <Route path="docs" element={<DeveloperDocsPage />} />
      <Route path="account" element={<AccountPage />} />
      <Route path="status" element={<StatusPage />} />
      <Route path="support" element={<SupportPage />} />
      <Route path="*" element={<NotFoundPage />} />
    </Route>
    {['workspace', 'api-keys', 'byok', 'provider-accounts', 'provider-health', 'usage', 'logs', 'recharge', 'subscription', 'billing', 'playground', 'account', 'support'].map((path) => <Route key={path} path={`/${path}`} element={<Navigate to={`/console/${path}`} replace />} />)}
    <Route path="/payments/success" element={<Navigate to="/console/payments/success" replace />} />
  </Routes></PublicSettingsProvider>
}
