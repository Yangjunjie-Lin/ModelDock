import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, Menu, Network, X } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom'
import { publicApi, type PublicConfig } from '../lib/public-api'
import { usePublicSettings } from '../lib/public-settings'

const primaryNavigation = [
  { to: '/product', label: '产品能力' },
  { to: '/models', label: '模型目录' },
  { to: '/providers', label: 'Provider 状态' },
  { to: '/pricing', label: '定价' },
  { to: '/docs', label: '开发者文档' },
  { to: '/status', label: '状态' },
]

export function PublicShell() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()
  const settings = usePublicSettings()
  const config = useQuery({ queryKey: ['public-config'], queryFn: () => publicApi<PublicConfig>('/config'), staleTime: 60_000 })
  const regions = useMemo(() => config.data?.supported_regions?.length ? config.data.supported_regions : [settings.region], [config.data, settings.region])
  const currencies = useMemo(() => config.data?.supported_currencies?.length ? config.data.supported_currencies : [settings.currency], [config.data, settings.currency])
  const selectionConfirmed = Boolean(config.data?.supported_regions?.length && config.data?.supported_currencies?.length)
  const startRoute = !config.data ? '/pricing' : config.data.registration_mode === 'CLOSED' ? '/contact' : '/register'
  const startLabel = config.data?.registration_mode === 'CLOSED' ? '咨询接入' : config.data ? '开始使用' : '核对可用性'

  useEffect(() => setMobileOpen(false), [location.pathname])
  useEffect(() => {
    if (config.data?.supported_regions?.length && !config.data.supported_regions.includes(settings.region)) settings.setRegion(config.data.supported_regions[0])
    if (config.data?.supported_currencies?.length && !config.data.supported_currencies.includes(settings.currency)) settings.setCurrency(config.data.supported_currencies[0])
  }, [config.data, settings])

  return <div className="public-site">
    <a className="skip-link" href="#main-content">跳到主要内容</a>
    <header className="public-header">
      <div className="public-nav-wrap">
        <Link className="public-brand" to="/" aria-label="ModelDock 首页"><span className="brand-mark"><Network size={18} /></span><span><strong>ModelDock</strong><small>AI gateway</small></span></Link>
        <nav id="public-navigation" className={mobileOpen ? 'public-nav open' : 'public-nav'} aria-label="主导航">
          {primaryNavigation.map((item) => <NavLink key={item.to} to={item.to} className={({ isActive }) => isActive ? 'active' : undefined}>{item.label}</NavLink>)}
          <div className="public-mobile-actions"><Link to="/contact">联系支持</Link><Link to="/enterprise">企业服务</Link><Link to="/login">登录</Link><Link className="public-cta" to={startRoute}>{startLabel}</Link></div>
        </nav>
        <div className="public-header-actions">
          {!selectionConfirmed && <span className="public-config-warning" title={config.isError ? '公开配置请求失败' : '公开配置未返回支持列表'}>地区/币种配置不可确认</span>}
          <label className="public-select"><span className="sr-only">查询地区</span><select aria-label="查询地区" value={settings.region} disabled={!selectionConfirmed} onChange={(event) => settings.setRegion(event.target.value)}>{regions.map((region) => <option value={region} key={region}>{region}</option>)}</select><ChevronDown size={12} /></label>
          <label className="public-select currency"><span className="sr-only">显示币种</span><select aria-label="显示币种" value={settings.currency} disabled={!selectionConfirmed} onChange={(event) => settings.setCurrency(event.target.value)}>{currencies.map((currency) => <option value={currency} key={currency}>{currency}</option>)}</select><ChevronDown size={12} /></label>
          <Link className="public-login" to="/login">登录</Link>
          <Link className="public-cta" to={startRoute}>{startLabel}</Link>
          <button className="public-menu-button" type="button" aria-expanded={mobileOpen} aria-controls="public-navigation" aria-label={mobileOpen ? '关闭导航' : '打开导航'} onClick={() => setMobileOpen((value) => !value)}>{mobileOpen ? <X size={19} /> : <Menu size={19} />}</button>
        </div>
      </div>
    </header>
    <main id="main-content" tabIndex={-1}><Outlet /></main>
    <footer className="public-footer">
      <div className="public-footer-grid">
        <div><Link className="public-brand" to="/"><span className="brand-mark"><Network size={18} /></span><span><strong>ModelDock</strong><small>RelayDock compatible</small></span></Link><p>面向开发者的 OpenAI-compatible 模型网关。实际能力、价格和地区可用性以本部署公开接口为准。</p></div>
        <div><strong>产品</strong><Link to="/product">产品能力</Link><Link to="/models">模型目录</Link><Link to="/providers">Provider 状态</Link><Link to="/pricing">定价</Link></div>
        <div><strong>开发者</strong><Link to="/docs">开发者文档</Link><Link to="/status">服务状态</Link><Link to="/contact">联系与支持</Link><Link to="/enterprise">企业服务</Link></div>
        <div><strong>法律与政策</strong><Link to="/legal/terms">服务条款</Link><Link to="/legal/privacy">隐私政策</Link><Link to="/legal/acceptable-use">可接受使用政策</Link><Link to="/legal/refunds">退款规则</Link><Link to="/legal/data-processing">数据处理说明</Link><Link to="/legal/providers">Provider 与模型披露</Link><Link to="/legal/complaints">投诉举报</Link><Link to="/legal/company">公司与备案信息</Link></div>
      </div>
      <div className="public-footer-bottom"><span>© {new Date().getFullYear()} ModelDock</span><span>法律页面均为待律师审核草案，不构成法律意见。</span></div>
    </footer>
  </div>
}
