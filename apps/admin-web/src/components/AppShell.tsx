import { useEffect, useMemo, useState } from 'react'
import {
  Activity,
  AlertTriangle,
  BarChart3,
  Bell,
  Building2,
  Boxes,
  Calculator,
  ChartNoAxesCombined,
  Cable,
  ChevronDown,
  CircleGauge,
  Command,
  FileClock,
  FileJson2,
  FolderKanban,
  Gauge,
  HeartPulse,
  Handshake,
  KeyRound,
  Layers3,
  Menu,
  MessageSquareMore,
  Megaphone,
  Moon,
  Network,
  Scale,
  PanelLeftClose,
  PanelLeftOpen,
  Route,
  Search,
  Settings,
  ShieldCheck,
  Store,
  Sun,
  Users,
  UserRoundCog,
  WalletCards,
  Webhook,
  X,
} from 'lucide-react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { LanguageToggle } from '../lib/i18n'
import { Modal, SearchInput } from './ui'

const navGroups = [
  {
    label: 'Workspace',
    items: [
      { to: '/', label: 'Dashboard', icon: CircleGauge },
      { to: '/organizations', label: 'Organizations', icon: Building2 },
      { to: '/projects', label: 'Projects', icon: FolderKanban },
      { to: '/teams', label: 'Teams', icon: Users },
      { to: '/providers', label: 'Providers', icon: Cable },
      { to: '/suppliers', label: 'Supplier Applications', icon: Handshake },
      { to: '/supplier-settlements', label: 'Supplier Settlements', icon: WalletCards },
      { to: '/marketplace', label: 'Marketplace', icon: Store },
      { to: '/credentials', label: 'Credential Pool', icon: ShieldCheck },
      { to: '/provider-accounts', label: 'Provider Accounts', icon: UserRoundCog },
      { to: '/provider-capabilities', label: 'Provider Capabilities', icon: FileJson2 },
      { to: '/groups', label: 'Groups', icon: Boxes },
    ],
  },
  {
    label: 'Gateway',
    items: [
      { to: '/models', label: 'Models', icon: Layers3 },
      { to: '/routes', label: 'Routes', icon: Route },
      { to: '/routing-rules', label: 'Routing Rules', icon: Route },
      { to: '/api-keys', label: 'API Keys', icon: KeyRound },
      { to: '/billing', label: 'Billing', icon: WalletCards },
      { to: '/payments', label: 'Payments', icon: WalletCards },
      { to: '/subscriptions', label: 'Subscriptions', icon: WalletCards },
      { to: '/pricing', label: 'Pricing', icon: Calculator },
      { to: '/finance', label: 'Finance', icon: ChartNoAxesCombined },
      { to: '/reconciliation', label: 'Reconciliation', icon: Scale },
      { to: '/governance', label: 'Governance', icon: ShieldCheck },
      { to: '/users', label: 'Users', icon: Users },
    ],
  },
  {
    label: 'Observability',
    items: [
      { to: '/usage', label: 'Usage Analytics', icon: BarChart3 },
      { to: '/request-logs', label: 'Request Logs', icon: Activity },
      { to: '/audit-logs', label: 'Audit Logs', icon: FileClock },
      { to: '/alerts', label: 'Alerts', icon: AlertTriangle },
      { to: '/status', label: 'Status & SLOs', icon: HeartPulse },
      { to: '/provider-quality', label: 'Provider Quality', icon: Gauge },
      { to: '/support', label: 'Support', icon: MessageSquareMore },
      { to: '/acquisition', label: 'Acquisition', icon: Megaphone },
      { to: '/webhooks', label: 'Webhooks', icon: Webhook },
    ],
  },
]

const allItems = navGroups.flatMap((group) => group.items)

function Logo() {
  return <div className="brand"><span className="brand-mark"><Network size={18} strokeWidth={2.2} /></span><span><strong>ModelDock</strong><small>AI Model Gateway</small></span></div>
}

export function AppShell() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [commandOpen, setCommandOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [theme, setTheme] = useState<'light' | 'dark'>(() => (localStorage.getItem('rd-theme') === 'light' ? 'light' : 'dark'))
  const location = useLocation()
  const navigate = useNavigate()
  const current = allItems.find((item) => item.to === location.pathname)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('rd-theme', theme)
  }, [theme])

  useEffect(() => {
    setMobileOpen(false)
  }, [location.pathname])

  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setCommandOpen(true)
      }
    }
    window.addEventListener('keydown', shortcut)
    return () => window.removeEventListener('keydown', shortcut)
  }, [])

  const results = useMemo(() => allItems.filter((item) => item.label.toLowerCase().includes(query.toLowerCase())), [query])
  const visit = (path: string) => { navigate(path); setCommandOpen(false); setQuery('') }

  return (
    <div className={`app-frame ${collapsed ? 'sidebar-collapsed' : ''}`}>
      {mobileOpen && <button className="mobile-scrim" onClick={() => setMobileOpen(false)} aria-label="Close navigation" />}
      <aside className={`sidebar ${mobileOpen ? 'mobile-open' : ''}`}>
        <div className="sidebar-top"><Logo /><button className="mobile-close" onClick={() => setMobileOpen(false)} aria-label="Close"><X size={18} /></button></div>
        <nav className="nav-scroll">
          {navGroups.map((group) => <div className="nav-group" key={group.label}><span className="nav-label">{group.label}</span>{group.items.map(({ to, label, icon: Icon }) => <NavLink key={to} to={to} end={to === '/'} title={collapsed ? label : undefined} className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}><Icon size={17} /><span>{label}</span></NavLink>)}</div>)}
        </nav>
        <div className="sidebar-bottom">
          <NavLink to="/settings" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}><Settings size={17} /><span>Settings</span></NavLink>
          <div className="admin-profile"><span className="avatar">AD</span><span className="profile-copy"><strong>Administrator</strong><small>Admin workspace</small></span><ChevronDown size={14} /></div>
        </div>
      </aside>
      <main className="main-shell">
        <header className="topbar">
          <div className="topbar-left">
            <button className="icon-button mobile-menu" onClick={() => setMobileOpen(true)} aria-label="Open navigation"><Menu size={18} /></button>
            <button className="icon-button desktop-collapse" onClick={() => setCollapsed(!collapsed)} aria-label="Toggle sidebar">{collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}</button>
            <div className="breadcrumb"><span>ModelDock</span><b>/</b><strong>{current?.label || (location.pathname === '/settings' ? 'Settings' : 'Admin')}</strong></div>
          </div>
          <div className="topbar-actions">
            <button className="command-trigger" onClick={() => setCommandOpen(true)}><Search size={15} /><span>Search</span><kbd><Command size={11} /> K</kbd></button>
            <LanguageToggle compact />
            <button className="icon-button" onClick={() => navigate('/alerts')} aria-label="Alerts"><Bell size={17} /></button>
            <button className="icon-button" onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')} aria-label="Toggle theme">{theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}</button>
          </div>
        </header>
        <div className="content"><Outlet /></div>
      </main>
      <Modal open={commandOpen} onClose={() => setCommandOpen(false)} title="Go to" description="Search ModelDock administration pages.">
        <SearchInput autoFocus value={query} onChange={setQuery} placeholder="Type a page name…" />
        <div className="command-results">{results.map(({ to, label, icon: Icon }) => <button key={to} onClick={() => visit(to)}><span><Icon size={16} />{label}</span><small>Open</small></button>)}{results.length === 0 && <p>No matching pages.</p>}</div>
      </Modal>
    </div>
  )
}
