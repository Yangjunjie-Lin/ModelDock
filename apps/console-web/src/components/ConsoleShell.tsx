import { useEffect, useMemo, useState } from 'react'
import { BarChart3, BookOpen, Braces, ChevronDown, Command, FileSearch, KeyRound, Layers3, LayoutDashboard, Menu, Moon, Network, PanelLeftClose, PanelLeftOpen, Search, Sun, X } from 'lucide-react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useProjectScope } from '../lib/project-scope'
import { LanguageToggle } from '../lib/i18n'
import { Modal, SearchInput } from './ui'

const nav = [
  { to: '/', label: 'Overview', icon: LayoutDashboard },
  { to: '/api-keys', label: 'API Keys', icon: KeyRound },
  { to: '/models', label: 'Models', icon: Layers3 },
  { to: '/usage', label: 'Usage', icon: BarChart3 },
  { to: '/logs', label: 'Logs', icon: FileSearch },
  { to: '/playground', label: 'Playground', icon: Braces },
  { to: '/docs', label: 'Documentation', icon: BookOpen },
]

export function ConsoleShell() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [commandOpen, setCommandOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [theme, setTheme] = useState<'light' | 'dark'>(() => localStorage.getItem('rd-theme') === 'light' ? 'light' : 'dark')
  const location = useLocation()
  const navigate = useNavigate()
  const scope = useProjectScope()
  const current = nav.find((item) => item.to === location.pathname)
  const results = useMemo(() => nav.filter((item) => item.label.toLowerCase().includes(query.toLowerCase())), [query])

  useEffect(() => { document.documentElement.dataset.theme = theme; localStorage.setItem('rd-theme', theme) }, [theme])
  useEffect(() => { setMobileOpen(false) }, [location.pathname])
  useEffect(() => { const handler = (event: KeyboardEvent) => { if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); setCommandOpen(true) } }; addEventListener('keydown', handler); return () => removeEventListener('keydown', handler) }, [])

  return <div className={`app-frame console-frame ${collapsed ? 'sidebar-collapsed' : ''}`}>
    {mobileOpen && <button className="mobile-scrim" onClick={() => setMobileOpen(false)} aria-label="Close navigation" />}
    <aside className={`sidebar ${mobileOpen ? 'mobile-open' : ''}`}>
      <div className="sidebar-top"><div className="brand"><span className="brand-mark"><Network size={18} /></span><span><strong>ModelDock</strong><small>Developer console</small></span></div><button className="mobile-close" onClick={() => setMobileOpen(false)} aria-label="Close"><X size={18} /></button></div>
      <div className="workspace-switcher project-switcher" title={scope.error instanceof Error ? scope.error.message : undefined}><span className="avatar">{scope.project?.name.slice(0, 2).toUpperCase() || 'WS'}</span><span><label><small>Organization</small><select aria-label="Organization" value={scope.organizationID} onChange={(event) => scope.setOrganizationID(event.target.value)} disabled={scope.loading}><option value="">Select organization</option>{scope.organizations.map((organization) => <option value={organization.id} key={organization.id}>{organization.name}</option>)}</select></label><label><small>Project</small><select aria-label="Project" value={scope.projectID} onChange={(event) => scope.setProjectID(event.target.value)} disabled={!scope.organizationID || scope.loading}><option value="">Select project</option>{scope.projects.map((project) => <option value={project.id} key={project.id}>{project.name}</option>)}</select></label></span><ChevronDown size={14} /></div>
      <nav className="nav-scroll"><div className="nav-group"><span className="nav-label">Build</span>{nav.slice(0, 5).map(({ to, label, icon: Icon }) => <NavLink key={to} to={to} end={to === '/'} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} title={collapsed ? label : undefined}><Icon size={17} /><span>{label}</span></NavLink>)}</div><div className="nav-group"><span className="nav-label">Developer</span>{nav.slice(5).map(({ to, label, icon: Icon }) => <NavLink key={to} to={to} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} title={collapsed ? label : undefined}><Icon size={17} /><span>{label}</span></NavLink>)}</div></nav>
      <div className="sidebar-bottom"><div className="console-user"><span className="avatar">U</span><span className="profile-copy"><strong>ModelDock user</strong><small>Console access</small></span><ChevronDown size={14} /></div></div>
    </aside>
    <main className="main-shell">
      <header className="topbar"><div className="topbar-left"><button className="icon-button mobile-menu" onClick={() => setMobileOpen(true)} aria-label="Open navigation"><Menu size={18} /></button><button className="icon-button desktop-collapse" onClick={() => setCollapsed(!collapsed)} aria-label="Toggle sidebar">{collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}</button><div className="breadcrumb"><span>Console</span><b>/</b><strong>{current?.label || 'Workspace'}</strong></div></div><div className="topbar-actions"><button className="command-trigger" onClick={() => setCommandOpen(true)}><Search size={15} /><span>Search console</span><kbd><Command size={11} /> K</kbd></button><LanguageToggle compact /><button className="icon-button" onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')} aria-label="Toggle theme">{theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}</button></div></header>
      <div className="content console-content"><Outlet /></div>
    </main>
    <Modal open={commandOpen} onClose={() => setCommandOpen(false)} title="Search console" description="Jump to a ModelDock console page."><SearchInput autoFocus value={query} onChange={setQuery} placeholder="Search pages…" /><div className="command-results">{results.map(({ to, label, icon: Icon }) => <button key={to} onClick={() => { navigate(to); setCommandOpen(false); setQuery('') }}><span><Icon size={16} />{label}</span><small>Open</small></button>)}</div></Modal>
  </div>
}
