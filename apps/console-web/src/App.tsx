import { Navigate, Route, Routes } from 'react-router-dom'
import { ConsoleShell } from './components/ConsoleShell'
import { ApiKeysPage } from './pages/ApiKeysPage'
import { LogsPage, ModelsPage, NotFoundPage, UsagePage } from './pages/DataPages'
import { DocsPage } from './pages/DocsPage'
import { LoginPage } from './pages/LoginPage'
import { OverviewPage } from './pages/OverviewPage'
import { PlaygroundPage } from './pages/PlaygroundPage'
import { ProjectScopeProvider } from './lib/project-scope'

export function App() {
  return <Routes><Route path="/login" element={<LoginPage />} /><Route element={<ProjectScopeProvider><ConsoleShell /></ProjectScopeProvider>}><Route index element={<OverviewPage />} /><Route path="api-keys" element={<ApiKeysPage />} /><Route path="models" element={<ModelsPage />} /><Route path="usage" element={<UsagePage />} /><Route path="logs" element={<LogsPage />} /><Route path="playground" element={<PlaygroundPage />} /><Route path="docs" element={<DocsPage />} /><Route path="console/*" element={<Navigate to="/" replace />} /><Route path="*" element={<NotFoundPage />} /></Route></Routes>
}
