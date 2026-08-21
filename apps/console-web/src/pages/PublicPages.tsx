import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Activity, ArrowRight, BadgeCheck, BookOpen, Building2, CheckCircle2, CircleDollarSign,
  Clock3, CloudCog, Code2, Database, ExternalLink, Globe2, Headphones, KeyRound, Layers3, LifeBuoy,
  LockKeyhole, Mail, MapPin, Network, RefreshCw, Search, ShieldCheck, TriangleAlert, WalletCards,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { Badge, Button, EmptyState, ErrorState, Skeleton, StatusBadge } from '../components/ui'
import { formatDate, formatMoney, formatNumber } from '../lib/api'
import { trackHomepageVisit } from '../lib/funnel'
import {
  approvedPaymentChannelFee,
  commercialTermsApproved,
  deliverablePublicEmail,
  publicApi,
  isPublicModelAvailable,
  isPublicProviderAvailable,
  publicModelAllowedRegions,
  publicTokenPriceName,
  type PublicConfig,
  type PublicModel,
  type PublicModelCatalog,
  type PublicPricing,
  type PublicProvider,
  type PublicProviderCatalog,
  type PublicStatus,
} from '../lib/public-api'
import { usePublicSettings } from '../lib/public-settings'

function usePublicContactEmails() {
  const config = useQuery({ queryKey: ['public-config'], queryFn: () => publicApi<PublicConfig>('/config'), staleTime: 60_000 })
  const supportEmail = deliverablePublicEmail(config.data?.support_email)
  const enterpriseEmail = deliverablePublicEmail(config.data?.enterprise_email) || supportEmail
  return { config, supportEmail, enterpriseEmail }
}

function PublicSection({ eyebrow, title, description, children, className = '' }: { eyebrow?: string; title: string; description?: string; children: ReactNode; className?: string }) {
  return <section className={`public-section ${className}`}><div className="public-container"><div className="public-section-head">{eyebrow && <span>{eyebrow}</span>}<h2>{title}</h2>{description && <p>{description}</p>}</div>{children}</div></section>
}

function PublicLoading() {
  return <div className="public-data-state" aria-live="polite"><Skeleton rows={5} /></div>
}

function PublicFailure({ error, retry, label }: { error: unknown; retry: () => void; label: string }) {
  return <div className="public-data-state"><ErrorState error={error} onRetry={retry} /><p>{label} 当前不可确认，因此相关购买或接入入口不会显示为可用。请稍后重试或联系支持。</p></div>
}

function AvailabilityBadge({ available, reason }: { available: boolean; reason?: string }) {
  return available ? <Badge tone="success" dot>当前地区可用</Badge> : <span className="availability-unavailable"><Badge tone="danger" dot>当前地区不可用</Badge>{reason && <small>{reason.replaceAll('_', ' ')}</small>}</span>
}

function RegionNotice() {
  const { region, currency } = usePublicSettings()
  return <div className="public-region-notice"><MapPin size={15} /><span>当前查询：地区 <strong>{region}</strong>，币种 <strong>{currency}</strong>。切换页首选项后，目录和价格会重新向服务端查询。</span></div>
}

function taxSummary(terms?: PublicPricing['commercial_terms']) {
  if (!terms) return '未披露/待确认'
  const subscription = terms.subscription_tax_included ?? terms.tax_included
  const token = terms.token_tax_included ?? terms.tax_included
  if (subscription === undefined || subscription === null || token === undefined || token === null) return '未披露/待确认'
  if (subscription === token) return subscription ? '订阅与 Token 含税' : '订阅与 Token 未含税'
  return `订阅${subscription ? '含税' : '未含税'} · Token${token ? '含税' : '未含税'}`
}

function isZeroDecimal(value: string) {
  return /^0+(?:\.0+)?$/.test(value.trim())
}

export function HomePage() {
  const settings = usePublicSettings()
  const config = useQuery({ queryKey: ['public-config'], queryFn: () => publicApi<PublicConfig>('/config'), staleTime: 60_000 })
  const models = useQuery({ queryKey: ['public-models', settings.region, settings.currency], queryFn: () => publicApi<PublicModelCatalog>('/catalog/models', { query: { region: settings.region, currency: settings.currency } }) })
  const providers = useQuery({ queryKey: ['public-providers', settings.region], queryFn: () => publicApi<PublicProviderCatalog>('/catalog/providers', { query: { region: settings.region } }) })
  const pricing = useQuery({ queryKey: ['public-pricing', settings.region, settings.currency], queryFn: () => publicApi<PublicPricing>('/pricing', { query: { region: settings.region, currency: settings.currency } }) })

  useEffect(() => trackHomepageVisit(), [])

  const availableModels = models.data?.items.filter((model) => isPublicModelAvailable(model) && model.pricing) || []
  const availableProviders = providers.data?.items.filter(isPublicProviderAvailable) || []
  const registrationOpen = config.data?.registration_mode === 'PUBLIC' || config.data?.registration_mode === 'INVITE_ONLY'
  const canStart = Boolean(config.data && registrationOpen && availableModels.length > 0 && commercialTermsApproved(pricing.data?.commercial_terms) && approvedPaymentChannelFee(pricing.data) && pricing.data?.payment_region_supported === true)
  const startLabel = config.data?.registration_mode === 'INVITE_ONLY' ? '使用邀请注册' : '创建账户'
  return <>
    <section className="public-hero"><div className="public-container public-hero-grid"><div className="public-hero-copy"><span className="public-kicker"><i />OPENAI-COMPATIBLE MODEL GATEWAY</span><h1>从真实价格与地区可用性，走到你的第一次 API 调用。</h1><p>用项目级 <code>rdk_*</code> 密钥连接 `/v1`，在调用前查看当前部署实际发布的模型、Provider 状态与计费条件。</p><div className="public-hero-actions">{canStart ? <Link className="public-button primary" to="/register">{startLabel}<ArrowRight size={16} /></Link> : <Link className="public-button primary" to="/pricing">先核对可用性与价格<ArrowRight size={16} /></Link>}<Link className="public-button" to="/docs"><BookOpen size={16} />查看接入文档</Link></div><div className="public-hero-proof"><span><CheckCircle2 size={15} />保留 OpenAI-compatible `/v1`</span><span><CheckCircle2 size={15} />十进制计价展示</span><span><CheckCircle2 size={15} />Provider 与地区静态准入披露</span></div></div><div className="public-terminal" aria-label="OpenAI SDK 接入示例"><header><span /><span /><span /><b>first-request.py</b></header><pre><code><em>from</em> openai <em>import</em> OpenAI{`\n\n`}client = OpenAI({`\n`}  api_key=os.environ[<q>"RELAYDOCK_API_KEY"</q>],{`\n`}  base_url=<q>"https://your-modeldock.example/v1"</q>{`\n`}){`\n\n`}response = client.responses.create({`\n`}  model=<q>"&lt;published-model-alias&gt;"</q>,{`\n`}  input=<q>"Hello, ModelDock"</q>{`\n`})</code></pre><footer><span><i />请求会使用你的组织、项目和价格快照结算</span></footer></div></div></section>
    <section className="public-truth-strip"><div className="public-container"><div><strong>{models.isSuccess ? formatNumber(availableModels.length, false) : '—'}</strong><span>{settings.region} 静态准入且已发布价格的模型</span></div><div><strong>{providers.isSuccess ? formatNumber(availableProviders.length, false) : '—'}</strong><span>{settings.region} 通过公开静态准入的 Provider</span></div><div><strong>{pricing.data?.subscription_plans.length ?? '—'}</strong><span>服务端当前发布套餐</span></div><div><strong>{taxSummary(pricing.data?.commercial_terms)}</strong><span>当前地区税费口径</span></div></div></section>
    <PublicSection eyebrow="CAPABILITIES" title="一条可核验的商业接入路径" description="目录和金额来自公开接口；网络失败或数据缺失时，页面明确停止展示可购买状态。">
      <div className="public-feature-grid"><Feature icon={<Layers3 />} title="真实模型目录" text="展示 Provider、能力、上下文窗口、允许地区与当前发布证据。" link="/models" /><Feature icon={<CircleDollarSign />} title="调用前价格披露" text="订阅费、Token 按量费、支付渠道费、赠送额度、税费与退款条件分别呈现。" link="/pricing" /><Feature icon={<CloudCog />} title="Provider 静态准入" text="available 仅反映开关、合同期、转售授权与地区规则，不包含项目授权、价格、凭据或实时健康保证。" link="/providers" /><Feature icon={<KeyRound />} title="项目级密钥" text="创建只展示一次的 rdk_* 密钥；密钥策略、用量和请求日志按项目隔离。" link="/console/onboarding" /><Feature icon={<WalletCards />} title="可追溯计费" text="充值、订阅与 Token 扣费分开记录；控制台展示余额、请求费用与账本关联。" link="/console/billing" /><Feature icon={<ShieldCheck />} title="组织自有 BYOK" text="仅接受组织拥有或获授权的官方 Provider 凭据，不支持密钥交易或地区规避。" link="/console/byok" /></div>
    </PublicSection>
    <PublicSection eyebrow="LIVE AVAILABILITY" title={`购买前先检查 ${settings.region} 可用性`} description="下列摘要直接来自公开目录；空数据不代表可用。" className="public-section-alt">
      <RegionNotice />
      {(models.isLoading || providers.isLoading) && <PublicLoading />}
      {(models.isError || providers.isError) && <PublicFailure error={models.error || providers.error} retry={() => { void models.refetch(); void providers.refetch() }} label="模型或 Provider 状态" />}
      {models.isSuccess && providers.isSuccess && <div className="public-live-grid"><div><span>已发布模型</span><strong>{models.data.items.length}</strong><small>{availableModels.length} 个通过地区/价格静态准入；调用时仍需项目与健康准入</small><Link to="/models">打开模型目录<ArrowRight size={14} /></Link></div><div><span>Provider</span><strong>{providers.data.items.length}</strong><small>{availableProviders.length} 个通过开关、合同期、转售授权与地区静态准入；不代表已有价格或实时健康</small><Link to="/providers">查看 Provider 证据<ArrowRight size={14} /></Link></div><div><span>定价条款</span><strong>{pricing.data?.commercial_terms?.legal_review_status === 'APPROVED' ? '已审核发布' : pricing.data?.commercial_terms?.legal_review_status === 'PENDING' ? '待律师审核' : '不可确认'}</strong><small>{pricing.data?.updated_at ? `更新于 ${formatDate(pricing.data.updated_at)}` : '未返回更新时间'}</small><Link to="/pricing">核对完整价格<ArrowRight size={14} /></Link></div></div>}
    </PublicSection>
    <PublicSection eyebrow="GET STARTED" title="完成第一次调用，不需要阅读内部文档" description="公开文档、控制台向导与运行状态使用同一套服务端事实。">
      <ol className="public-steps"><li><span>01</span><div><strong>注册并验证邮箱</strong><p>注册策略由当前部署公开配置决定。</p></div></li><li><span>02</span><div><strong>创建组织并选套餐</strong><p>在选购前确认地区、Token 价格与商业条款。</p></div></li><li><span>03</span><div><strong>充值并创建 API Key</strong><p>完整密钥仅展示一次，请保存到密钥管理器。</p></div></li><li><span>04</span><div><strong>复制示例并检查扣费</strong><p>首次调用后在用量与账务页核对请求和价格快照。</p></div></li></ol>
      <div className="public-centered-actions"><Link className="public-button primary" to={canStart ? '/register' : '/pricing'}>{canStart ? '开始 onboarding' : '查看当前可用性'}<ArrowRight size={16} /></Link><Link className="public-button" to="/status">查看服务状态</Link></div>
    </PublicSection>
  </>
}

function Feature({ icon, title, text, link }: { icon: ReactNode; title: string; text: string; link: string }) {
  return <article className="public-feature"><span>{icon}</span><h3>{title}</h3><p>{text}</p><Link to={link}>了解更多<ArrowRight size={14} /></Link></article>
}

export function ProductPage() {
  return <><PublicPageHero eyebrow="PRODUCT" title="把模型路由、资金边界与开发者体验放在一条可审计链路上。" description="ModelDock 保留 RelayDock 的 OpenAI-compatible 数据面，同时增加组织、项目、价格、Provider 商业准入和账务能力。" /><PublicSection title="当前产品能力" description="以下仅描述仓库中已实现并可由接口核验的能力。"><div className="public-capability-list"><Capability icon={<Code2 />} title="OpenAI-compatible 数据面" bullets={['GET /v1/models', 'POST /v1/responses', 'POST /v1/chat/completions', 'POST /v1/embeddings 与 SSE']} /><Capability icon={<Network />} title="模型与 Provider 路由" bullets={['稳定模型别名', '项目级路由授权', '健康凭据选择', '受约束的 fallback group']} /><Capability icon={<LockKeyhole />} title="访问边界" bullets={['rdk_live_* 与 rdk_test_*', '项目级密钥与模型范围', 'RPM / TPM 限流', 'HttpOnly 控制台会话与 CSRF']} /><Capability icon={<Database />} title="计费与证据" bullets={['NUMERIC 精确金额', '请求价格快照', '充值、订阅与 Token 分账', '审计与账本关联']} /><Capability icon={<Globe2 />} title="商业与地区准入" bullets={['Provider 合同状态', '允许与禁止地区', '数据处理地区', '禁用与 kill switch']} /><Capability icon={<LifeBuoy />} title="运行支持" bullets={['公开状态与事件历史', '请求 ID 调查入口', '脱敏工单上下文', '用量与账务导出']} /></div></PublicSection><PublicSection title="能力边界" className="public-section-alt"><div className="public-boundary"><TriangleAlert size={20} /><div><strong>ModelDock 不承诺所有模型、所有 Provider 或所有地区始终可用。</strong><p>实际可用性取决于本部署配置、Provider 合同、地区准入、模型授权、资金状态和实时健康状态。Provider fallback 仅在已配置且满足商业与安全约束时执行。</p></div></div></PublicSection></>
}

function Capability({ icon, title, bullets }: { icon: ReactNode; title: string; bullets: string[] }) {
  return <article><span>{icon}</span><div><h3>{title}</h3><ul>{bullets.map((bullet) => <li key={bullet}><CheckCircle2 size={14} />{bullet}</li>)}</ul></div></article>
}

export function ModelsCatalogPage() {
  const settings = usePublicSettings()
  const [search, setSearch] = useState('')
  const [onlyAvailable, setOnlyAvailable] = useState(false)
  const query = useQuery({ queryKey: ['public-models', settings.region, settings.currency], queryFn: () => publicApi<PublicModelCatalog>('/catalog/models', { query: { region: settings.region, currency: settings.currency } }) })
  const rows = useMemo(() => (query.data?.items || []).filter((model) => {
    const matches = `${model.display_name} ${model.id} ${model.provider_name} ${model.capabilities.join(' ')}`.toLowerCase().includes(search.trim().toLowerCase())
    return matches && (!onlyAvailable || isPublicModelAvailable(model))
  }), [onlyAvailable, query.data, search])
  return <><PublicPageHero eyebrow="MODEL CATALOG" title="先确认地区、模型与价格，再开始调用。" description="模型目录按地区和币种查询当前发布证据，并显式保留 unavailable 项；这不是项目授权、凭据健康或实时可调用保证。"><RegionNotice /></PublicPageHero><PublicSection title="已发布模型" description={query.data?.updated_at ? `服务端更新时间：${formatDate(query.data.updated_at)}` : '仅展示当前公开接口返回的数据。'}><div className="public-catalog-toolbar"><label><Search size={16} /><span className="sr-only">搜索模型</span><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索模型、Provider 或能力" /></label><label className="public-checkbox"><input type="checkbox" checked={onlyAvailable} onChange={(event) => setOnlyAvailable(event.target.checked)} />仅显示当前地区静态准入</label><Button size="sm" onClick={() => query.refetch()}><RefreshCw size={13} />刷新</Button></div>{query.isLoading && <PublicLoading />}{query.isError && <PublicFailure error={query.error} retry={() => void query.refetch()} label="模型目录" />}{query.isSuccess && !rows.length && <EmptyState title="当前筛选没有已发布模型" description="这不表示其他地区或未公开模型可用。请切换查询条件或联系支持。" />}{rows.length > 0 && <div className="public-model-grid">{rows.map((model) => <ModelCard model={model} key={model.id} />)}</div>}</PublicSection></>
}

function ModelCard({ model }: { model: PublicModel }) {
  const available = isPublicModelAvailable(model)
  return <article className="public-model-card"><header><div><span>{model.provider_name}</span><h2>{model.display_name}</h2><code>{model.provider_model_id}</code></div><AvailabilityBadge available={available} reason={model.availability.reason_code || model.availability.status} /></header><div className="public-model-meta"><span>类型<strong>{model.model_type || '未披露'}</strong></span><span>上下文<strong>{model.context_window ? formatNumber(model.context_window) : '未披露'}</strong></span><span>内容标签能力<strong>{model.generated_content_label_capability || '未披露'}</strong></span></div><div className="public-model-capabilities">{model.capabilities.length ? model.capabilities.map((capability) => <Badge key={capability}>{capability}</Badge>) : <span>未发布能力标签</span>}</div><div className="public-price-box">{model.pricing ? <><div><span>输入 Token</span><strong>{formatMoney(model.pricing.input_token_price, model.pricing.currency)}</strong></div><div><span>缓存输入</span><strong>{formatMoney(model.pricing.cached_input_token_price, model.pricing.currency)}</strong></div><div><span>输出 Token</span><strong>{formatMoney(model.pricing.output_token_price, model.pricing.currency)}</strong></div><small>计价单位：{model.pricing.unit} · 固定请求费 {formatMoney(model.pricing.request_fixed_price, model.pricing.currency)}</small></> : <div className="public-price-missing"><TriangleAlert size={16} /><span>未发布可核验价格，因此不能视为可购买。</span></div>}</div><footer><span>允许地区：{publicModelAllowedRegions(model).join(', ') || '未披露'}</span>{available && model.pricing ? <Link to="/register">开始接入<ArrowRight size={14} /></Link> : <Link to="/contact">咨询可用性</Link>}</footer></article>
}

export function ProvidersPage() {
  const { region } = usePublicSettings()
  const query = useQuery({ queryKey: ['public-providers', region], queryFn: () => publicApi<PublicProviderCatalog>('/catalog/providers', { query: { region } }) })
  return <><PublicPageHero eyebrow="PROVIDER STATUS" title="公开商业与地区证据，不是当前可调用保证。" description="available=true 仅表示开关、合同期、转售授权与查询地区的静态准入通过；它不检查零售价、项目授权、可用凭据或实时健康。实时组件状态请另看状态页。"><RegionNotice /></PublicPageHero><PublicSection title="Provider 目录" description={query.data?.updated_at ? `服务端更新时间：${formatDate(query.data.updated_at)}` : '读取当前部署的公开 Provider 发布证据。'}>{query.isLoading && <PublicLoading />}{query.isError && <PublicFailure error={query.error} retry={() => void query.refetch()} label="Provider 状态" />}{query.isSuccess && !query.data.items.length && <EmptyState title="当前地区没有公开 Provider" description="未返回数据不会被解释为可用。" />}{query.data?.items.length ? <div className="public-provider-list">{query.data.items.map((provider) => <ProviderRow provider={provider} key={provider.id} />)}</div> : null}</PublicSection><PublicSection title="如何理解静态准入" className="public-section-alt"><div className="public-rule-grid"><div><BadgeCheck size={20} /><strong>商业证据</strong><p>开关、合同期、转售授权和条款版本需处于服务端允许状态。</p></div><div><Globe2 size={20} /><strong>地区证据</strong><p>公开目录按查询地区披露 Provider 和模型的允许/禁止规则。</p></div><div><Activity size={20} /><strong>调用时重新准入</strong><p>目录不评估价格、项目授权、凭据健康或实时容量；实际调用会重新执行完整准入，状态页另行披露运行事件。</p></div></div></PublicSection></>
}

function ProviderRow({ provider }: { provider: PublicProvider }) {
  return <article className="public-provider-row"><div className="provider-monogram">{provider.name.slice(0, 2).toUpperCase()}</div><div className="provider-primary"><h2>{provider.name}</h2><p>{provider.provider_type} · 条款版本 {provider.terms_version || '未披露'}</p><div><StatusBadge value={provider.commercial_status} /><StatusBadge value={provider.commercial_resale_status || provider.resale_status} />{(provider.emergency_kill_switch ?? provider.kill_switch) && <Badge tone="danger">kill switch 已开启</Badge>}</div></div><div className="provider-details"><span>已启用模型<strong>{formatNumber(provider.enabled_model_count ?? provider.available_model_count, false)}</strong></span><span>允许地区<strong>{(provider.allowed_customer_regions || provider.allowed_regions)?.join(', ') || '未披露'}</strong></span><span>数据处理地区<strong>{provider.data_processing_regions?.join(', ') || '未披露'}</strong></span><span>数据保留<strong>{provider.data_retention_policy || '未披露'}</strong></span><span>平台技术状态<strong>{provider.technical_status || 'UNKNOWN'}</strong></span><span>平台质量等级<strong>{provider.quality_grade || 'UNKNOWN'}</strong></span><span>平台实测 uptime<strong>{provider.published_uptime ? `${provider.published_uptime}%` : '证据不足'}</strong></span><span>供应商声明 uptime<strong>{provider.declared_uptime || '未声明'}</strong></span></div><AvailabilityBadge available={isPublicProviderAvailable(provider)} reason={provider.availability?.reason_code || provider.availability_reason} /></article>
}

export function PricingPage() {
  const settings = usePublicSettings()
  const query = useQuery({ queryKey: ['public-pricing', settings.region, settings.currency], queryFn: () => publicApi<PublicPricing>('/pricing', { query: { region: settings.region, currency: settings.currency } }) })
  const terms = query.data?.commercial_terms
  const selfServiceReady = Boolean(commercialTermsApproved(terms) && approvedPaymentChannelFee(query.data) && query.data?.payment_region_supported === true && query.data.token_prices.some((price) => price.availability.available))
  return <>
    <PublicPageHero eyebrow="PRICING" title="订阅、Token、支付渠道费与商业条款分别披露。" description="所有金额均来自服务端 NUMERIC/DECIMAL 的 JSON 字符串；页面不内置模拟价格。"><RegionNotice /></PublicPageHero>
    <PublicSection title="当前公开定价" description={query.data?.updated_at ? `服务端更新时间：${formatDate(query.data.updated_at)}` : '价格接口失败或未发布条款时，购买入口保持不可用。'}>
      {query.isLoading && <PublicLoading />}
      {query.isError && <PublicFailure error={query.error} retry={() => void query.refetch()} label="定价" />}
      {query.isSuccess && <div className="public-pricing-stack">
        <PricingDisclosure terms={terms} />
        {query.data.payment_region_supported !== true && <div className="public-explicit-empty"><TriangleAlert size={17} /><span>{query.data.payment_region_supported === false ? '当前地区没有同时满足运行时启用、合同准入与已审核费率披露的支付渠道' : '服务端未返回明确的支付地区/渠道状态'}，自助购买与充值不可用。</span></div>}
        {!approvedPaymentChannelFee(query.data) && <div className="public-explicit-empty"><TriangleAlert size={17} /><span>当前地区没有已记录律师审核批准的支付渠道费证据；平台服务费或待审核记录不能替代所选支付渠道披露，自助购买不可用。</span></div>}
        <div>
          <div className="public-subheading"><span>01</span><div><h3>订阅费</h3><p>订阅权益与 Token 按量费用分开；套餐不默认包含 Token 消耗。</p></div></div>
          {query.data.subscription_plans.length ? <div className="public-plan-grid">{query.data.subscription_plans.map((plan) => <article key={plan.plan_version_id}><header><span>{plan.slug}</span><h3>{plan.name}</h3><p>{plan.description || '服务端未发布套餐说明。'}</p></header><div className="plan-price"><strong>{formatMoney(plan.subscription_fee, plan.currency)}</strong><span>/ {plan.billing_interval}</span></div><ul><li><CheckCircle2 size={14} />Token 计费：{plan.token_billing_mode}</li><li><CheckCircle2 size={14} />试用天数：{plan.trial_days}</li><li><CheckCircle2 size={14} />版本：{plan.version ?? '未披露'}</li></ul>{plan.contact_sales || plan.enterprise_contract ? <Link className="public-button" to="/enterprise">联系企业服务</Link> : selfServiceReady ? <Link className="public-button primary" to="/register">选择此套餐</Link> : <button className="public-button" disabled>自助购买不可用</button>}</article>)}</div> : <EmptyState title="当前地区未发布订阅套餐" />}
        </div>
        <div>
          <div className="public-subheading"><span>02</span><div><h3>Token 按量费用</h3><p>调用按模型价格快照计费，具体可用性仍需在模型目录核对。</p></div></div>
          {query.data.token_prices.length ? <div className="public-table-wrap"><table className="public-table"><thead><tr><th>模型 / Provider</th><th>地区状态</th><th>输入</th><th>缓存输入</th><th>输出</th><th>固定请求费</th><th>单位</th></tr></thead><tbody>{query.data.token_prices.map((price) => <tr key={price.price_book_id}><td><strong>{publicTokenPriceName(price)}</strong><small>{price.provider_name || price.provider_slug || price.provider_id}</small></td><td><TokenPriceAvailability price={price} /></td><td>{formatMoney(price.input_token_price, price.currency)}</td><td>{formatMoney(price.cached_input_token_price, price.currency)}</td><td>{formatMoney(price.output_token_price, price.currency)}</td><td>{formatMoney(price.request_fixed_price, price.currency)}</td><td>{price.unit}</td></tr>)}</tbody></table></div> : <EmptyState title="当前地区未发布 Token 价格" description="没有价格的模型不能被视为可购买。" />}
        </div>
        <div>
          <div className="public-subheading"><span>03</span><div><h3>支付渠道或平台服务费</h3><p>此项与订阅费和 Token 费分开列示。</p></div></div>
          {query.data.payment_fees.length ? <div className="public-fee-grid">{query.data.payment_fees.map((fee) => <PaymentFeeCard fee={fee} fallbackCurrency={settings.currency} fallbackRegion={settings.region} key={fee.id} />)}</div> : <div className="public-explicit-empty"><TriangleAlert size={17} /><span>服务端未返回支付渠道或平台服务费。请勿据此推断费用为零；创建支付订单前联系支持确认。</span></div>}
        </div>
      </div>}
    </PublicSection>
    <PublicSection title="价格与可用性边界" className="public-section-alt"><div className="public-boundary"><Globe2 size={20} /><div><strong>价格发布不等于模型在所有地区可用。</strong><p>购买和调用前请同时核对 <Link to="/models">模型目录</Link> 与 <Link to="/providers">Provider 状态</Link>。调用时仍会执行组织、模型、Provider、地区、资金和健康准入。</p></div></div></PublicSection>
  </>
}

function PricingDisclosure({ terms }: { terms?: PublicPricing['commercial_terms'] }) {
  if (!terms) return <div className="public-explicit-empty"><TriangleAlert size={18} /><span>当前地区未发布完整商业条款，因此无法确认赠送额度、税费和退款条件，购买入口不可用。</span></div>
  const subscriptionTax = terms.subscription_tax_included ?? terms.tax_included
  const tokenTax = terms.token_tax_included ?? terms.tax_included
  const taxLabel = (value: boolean | null | undefined) => value === true ? '含税' : value === false ? '未含税' : '未披露/待确认'
  return <>{terms.legal_review_status !== 'APPROVED' && <div className="public-explicit-empty"><TriangleAlert size={18} /><span>当前商业披露仍待律师审核，不能视为已批准或最终条款；自助购买保持不可用。</span></div>}<div className="pricing-disclosure-grid"><div><span>赠送额度（条款披露）</span><strong>{isZeroDecimal(terms.bonus_credit_amount) ? '无已发布赠送额度' : formatMoney(terms.bonus_credit_amount, terms.currency)}</strong><small>实际可用额度仅以账户 promotion_credit 与钱包账本入账为准，非自动发放承诺。{terms.bonus_non_refundable ? ' 已入账赠送额度不可现金退款。' : ''}</small></div><div><span>是否含税</span><strong>订阅 {taxLabel(subscriptionTax)} · Token {taxLabel(tokenTax)}</strong><small>{terms.tax_disclosure || terms.tax_description || `服务端税率值：${terms.tax_rate || '未披露'}`}</small></div><div><span>退款条件</span><strong>按资格审核</strong><small>{terms.refund_summary || '未发布摘要'}</small></div><div><span>法律状态</span><strong>{terms.legal_review_status === 'APPROVED' ? '已记录律师审核批准' : '待律师审核'}</strong><small>生效时间：{formatDate(terms.effective_at)}</small></div></div></>
}

function TokenPriceAvailability({ price }: { price: PublicPricing['token_prices'][number] }) {
  if (!price.availability) return <Badge tone="warning">未披露/待确认</Badge>
  return <AvailabilityBadge available={price.availability.available} reason={price.availability.reason_code || price.availability.status} />
}

function PaymentFeeCard({ fee, fallbackCurrency, fallbackRegion }: { fee: PublicPricing['payment_fees'][number]; fallbackCurrency: string; fallbackRegion: string }) {
  const fixedAmount = fee.fixed_amount ?? fee.fixed_fee
  const rateBPS = fee.rate_bps ?? fee.percentage_bps
  return <article><strong>{fee.name || fee.payment_provider || fee.provider || fee.fee_category || '支付渠道'}</strong><span>类别：{fee.fee_category || fee.fee_kind || fee.fee_type || '未披露'}</span><span>固定费用：{fixedAmount !== undefined ? formatMoney(fixedAmount, fee.currency || fallbackCurrency) : '未披露'}</span><span>费率：{rateBPS !== undefined ? `${rateBPS} bps` : fee.percentage_rate ?? '未披露'}</span><span>适用地区：{fee.region || fallbackRegion}</span><span>向客户收取：{fee.charged_to_customer === true ? '是' : fee.charged_to_customer === false ? '否' : '未披露'}</span><span>律师审核：{fee.legal_review_status === 'APPROVED' ? '已批准' : '待审核（不可用于自助购买放行）'}</span>{fee.description && <p>{fee.description}</p>}</article>
}

export function PublicStatusPage() {
  const query = useQuery({ queryKey: ['public-status'], queryFn: () => publicApi<PublicStatus>('/status'), refetchInterval: 30_000 })
  const components = query.data ? [{ name: 'Gateway', ...query.data.components.gateway }, { name: 'Console', ...query.data.components.dashboard }, { name: 'Billing', ...query.data.components.billing }, ...(query.data.components.providers || []).map((provider) => ({ ...provider, name: `Provider / ${provider.name}` }))] : []
  return <><PublicPageHero eyebrow="SERVICE STATUS" title="当前服务组件与公开事件历史。" description="状态页展示经过脱敏的客户可见信息；Provider Operational 表示商业准入与可调度凭据已就绪，不是持续上游探测，也不代表未来绝对稳定。"><Button onClick={() => query.refetch()}><RefreshCw size={14} />刷新</Button></PublicPageHero><PublicSection title={query.data ? `总体状态：${query.data.status.replaceAll('_', ' ')}` : '服务状态'} description={query.data?.updated_at ? `服务端更新时间：${formatDate(query.data.updated_at)}` : undefined}>{query.isLoading && <PublicLoading />}{query.isError && <PublicFailure error={query.error} retry={() => void query.refetch()} label="服务状态" />}{query.data && <><div className="public-status-grid">{components.map((component) => <article key={component.name}><span className={component.status === 'OPERATIONAL' ? 'status-dot operational' : 'status-dot degraded'}>{component.status === 'OPERATIONAL' ? <CheckCircle2 size={18} /> : <TriangleAlert size={18} />}</span><div><strong>{component.name}</strong><p>{component.message || component.status.replaceAll('_', ' ')}</p></div><StatusBadge value={component.status} /></article>)}</div><div className="public-incident-list"><h3>公开事件</h3>{query.data.events.length ? query.data.events.map((event) => <article key={event.id}><Clock3 size={17} /><div><strong>{event.summary}</strong><p>{event.public_message}</p><small>{event.component} · {formatDate(event.started_at)}</small></div><StatusBadge value={event.resolved_at ? 'RESOLVED' : event.status} /></article>) : <p className="public-muted">当前公开事件列表为空。</p>}</div></>}</PublicSection></>
}

export function ContactPage() {
  const { config, supportEmail } = usePublicContactEmails()
  return <><PublicPageHero eyebrow="CONTACT & SUPPORT" title="从请求 ID、订单或账本证据开始定位问题。" description="请勿在邮件、工单或聊天中提交 API Key、Provider 密钥、密码、支付凭据或敏感提示内容。" /><PublicSection title="选择支持入口"><div className="public-contact-grid"><article><LifeBuoy /><h2>已登录用户</h2><p>在控制台创建工单，可关联经过脱敏的请求 ID、订单或账本编号。</p><Link className="public-button primary" to="/console/support">进入支持工单<ArrowRight size={15} /></Link></article><article><Activity /><h2>运行故障</h2><p>先查看公开组件状态和事件历史，再附上 RelayDock 请求 ID 联系支持。</p><Link className="public-button" to="/status">查看状态页</Link></article><article><Mail /><h2>公开联系</h2><p>{supportEmail ? '用于账户登录前的一般咨询；敏感问题请使用登录后工单。' : config.isLoading ? '正在读取部署方公开联系配置。' : '当前部署尚未配置可投递的公开支持邮箱；请登录后创建工单。'}</p>{supportEmail ? <a className="public-button" href={`mailto:${supportEmail}`}>{supportEmail}<ExternalLink size={14} /></a> : <Link className="public-button" to="/login">登录控制台</Link>}</article><article><Building2 /><h2>企业服务</h2><p>合同、地区、数据处理、SLA 与专属路由需要单独评估并书面确认。</p><Link className="public-button" to="/enterprise">企业服务入口</Link></article></div></PublicSection><PublicSection title="投诉与举报" className="public-section-alt"><div className="public-boundary"><Headphones size={20} /><div><strong>举报滥用、隐私、账单或 API Key 风险</strong><p>登录后可在账户页提交分类举报；未登录且无法访问账户时使用已公开的支持邮箱。所有报告都不应包含密钥或完整提示/响应内容。</p><Link to="/legal/complaints">查看投诉举报说明<ArrowRight size={14} /></Link></div></div></PublicSection></>
}

export function EnterprisePage() {
  const { enterpriseEmail } = usePublicContactEmails()
  return <><PublicPageHero eyebrow="ENTERPRISE" title="用书面合同确认地区、数据处理与服务边界。" description="企业入口不预设全球可用、完全合规或绝对稳定；每项承诺需与实际部署、Provider 条款和双方合同一致。"><div className="public-hero-actions">{enterpriseEmail ? <a className="public-button primary" href={`mailto:${enterpriseEmail}?subject=ModelDock%20Enterprise%20Inquiry`}>联系企业团队<Mail size={15} /></a> : <Link className="public-button primary" to="/contact">联系支持<ArrowRight size={15} /></Link>}<Link className="public-button" to="/providers">核对 Provider</Link></div></PublicPageHero><PublicSection title="企业评估范围"><div className="public-enterprise-grid"><article><Globe2 /><h3>地区与 Provider</h3><p>确认组织地区、Provider 合同、转售授权、模型允许地区和数据处理地区。</p></article><article><ShieldCheck /><h3>安全与数据</h3><p>确认身份、项目隔离、BYOK 所有权、内容保留、导出与删除流程。</p></article><article><CircleDollarSign /><h3>商业条款</h3><p>分别约定订阅、Token、渠道服务费、税、赠送额度、结算和退款边界。</p></article><article><Activity /><h3>运行与支持</h3><p>依据实际容量约定支持等级、事件沟通和可衡量的服务目标；不使用绝对承诺。</p></article></div></PublicSection><PublicSection title="准备咨询材料" className="public-section-alt"><ul className="public-checklist"><li><CheckCircle2 />预计使用地区与数据处理要求</li><li><CheckCircle2 />模型、并发、RPM / TPM 与月度 Token 量</li><li><CheckCircle2 />平台托管凭据或组织自有 BYOK</li><li><CheckCircle2 />账单币种、税务主体与付款方式</li><li><CheckCircle2 />保留、审计、工单与事件响应要求</li></ul></PublicSection></>
}

export function PublicNotFoundPage() {
  return <PublicSection title="页面不存在"><EmptyState title="找不到该公开页面" description="请返回首页或打开开发者文档。" action={<Link className="public-button primary" to="/">返回首页</Link>} /></PublicSection>
}

function PublicPageHero({ eyebrow, title, description, children }: { eyebrow: string; title: string; description: string; children?: ReactNode }) {
  return <section className="public-page-hero"><div className="public-container"><span>{eyebrow}</span><h1>{title}</h1><p>{description}</p>{children}</div></section>
}
