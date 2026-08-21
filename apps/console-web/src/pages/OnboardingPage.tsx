import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, Check, CheckCircle2, Circle, CircleDollarSign, Copy, Globe2, KeyRound, MailCheck, Network, RefreshCw, Send, Sparkles, UserRoundCheck, WalletCards } from 'lucide-react'
import { Link } from 'react-router-dom'
import { Badge, Button, EmptyState, ErrorState, Form, Panel, Skeleton, SubmitButton, useToast } from '../components/ui'
import { api, asPage, formatMoney } from '../lib/api'
import { type ConsoleOrganization, type ConsoleProject, useProjectScope } from '../lib/project-scope'
import { approvedPaymentChannelFee, commercialTermsApproved, findPurchasablePublicModel, publicApi, type PublicModelCatalog, type PublicPricing } from '../lib/public-api'
import { usePublicSettings } from '../lib/public-settings'
import { deriveOnboardingFacts, type OnboardingFacts, type OnboardingState } from '../lib/onboarding'

type ProjectModel = { id?: string; alias?: string; display_name?: string; enabled?: boolean; provider_id?: string; upstream_model?: string }

const gatewayBase = `${window.location.origin}/v1`

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 48)
}

function isZeroDecimal(value: string) {
  return /^0+(?:\.0+)?$/.test(value.trim())
}

export function OnboardingPage() {
  const scope = useProjectScope()
  const settings = usePublicSettings()
  const client = useQueryClient()
  const toast = useToast()
  const [organization, setOrganization] = useState({ name: '', slug: '' })
  const [project, setProject] = useState({ name: '', slug: '' })
  const [keyName, setKeyName] = useState('First API request')
  const [secret, setSecret] = useState('')
  const [exampleCopied, setExampleCopied] = useState(false)

  const onboarding = useQuery({
    queryKey: ['console-onboarding', scope.organizationID, scope.projectID],
    queryFn: () => api<OnboardingState>('/onboarding', { query: { organization_id: scope.organizationID || undefined, project_id: scope.projectID || undefined } }),
    refetchInterval: 15_000,
  })
  const pricing = useQuery({
    queryKey: ['public-pricing', settings.region, settings.currency],
    queryFn: () => publicApi<PublicPricing>('/pricing', { query: { region: settings.region, currency: settings.currency } }),
  })
  const publicModels = useQuery({
    queryKey: ['public-models', settings.region, settings.currency],
    queryFn: () => publicApi<PublicModelCatalog>('/catalog/models', { query: { region: settings.region, currency: settings.currency } }),
  })
  const projectModels = useQuery({
    queryKey: ['onboarding-project-models', scope.projectID],
    queryFn: () => api<unknown>('/models', { query: { project_id: scope.projectID, enabled: true } }).then(asPage<ProjectModel>),
    enabled: Boolean(scope.projectID),
  })
  const pricedProjectRoutes = useMemo(() => (projectModels.data?.items || []).flatMap((route) => {
    if (route.enabled === false) return []
    const model = findPurchasablePublicModel(route, publicModels.data?.items || [])
    if (!model) return []
    return [{ route, model }]
  }), [projectModels.data, publicModels.data])
  const selectedProjectRoute = pricedProjectRoutes[0]
  const alias = selectedProjectRoute ? String(selectedProjectRoute.route.alias || selectedProjectRoute.route.id || '') : ''
  const organizationRegionReady = Boolean(scope.organization && scope.organization.billing_region === settings.region)

  const createOrganization = useMutation({
    mutationFn: () => api<ConsoleOrganization>('/organizations', { method: 'POST', body: JSON.stringify({ name: organization.name.trim(), slug: organization.slug.trim(), status: 'ACTIVE', billing_region: settings.region }) }),
    onSuccess: async (created) => {
      await client.invalidateQueries({ queryKey: ['console-organizations'] })
      scope.setOrganizationID(created.id)
      setOrganization({ name: '', slug: '' })
      toast('组织已创建')
      void onboarding.refetch()
    },
  })
  const createProject = useMutation({
    mutationFn: () => api<ConsoleProject>(`/organizations/${scope.organizationID}/projects`, { method: 'POST', body: JSON.stringify({ name: project.name.trim(), slug: project.slug.trim(), status: 'ACTIVE' }) }),
    onSuccess: async (created) => {
      await client.invalidateQueries({ queryKey: ['console-projects', scope.organizationID] })
      scope.setProjectID(created.id)
      setProject({ name: '', slug: '' })
      toast('项目已创建')
      void onboarding.refetch()
    },
  })
  const confirmOrganizationRegion = useMutation({
    mutationFn: () => {
      if (!scope.organization) throw new Error('请先选择组织')
      return api<ConsoleOrganization>(`/organizations/${scope.organization.id}`, {
        method: 'PUT',
        body: JSON.stringify({ billing_region: settings.region }),
      })
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['console-organizations'] })
      scope.refresh()
      toast(`组织购买/客户地区已确认：${settings.region}`)
      void onboarding.refetch()
    },
  })
  const choosePlan = useMutation({
    mutationFn: (versionID: string) => api(`/organizations/${scope.organizationID}/subscription/change`, {
      method: 'POST',
      body: JSON.stringify({ plan_version_id: versionID, mode: 'IMMEDIATE', use_trial: true, idempotency_key: crypto.randomUUID() }),
    }),
    onSuccess: () => {
      toast('套餐变更已提交，最终状态以订阅页为准')
      void onboarding.refetch()
      void client.invalidateQueries({ queryKey: ['console-subscription', scope.organizationID] })
    },
  })
  const createKey = useMutation({
    mutationFn: () => api<Record<string, unknown>>('/api-keys', {
      method: 'POST',
      body: JSON.stringify({ name: keyName.trim(), environment: 'live', organization_id: scope.organizationID, project_id: scope.projectID, allowed_models: [alias] }),
    }),
    onSuccess: (response) => {
      const value = String(response.secret || response.key || '')
      if (!value) {
        toast('密钥已创建，但服务端未返回可展示的完整密钥', 'danger')
        return
      }
      setSecret(value)
      toast('API Key 已创建；完整密钥仅显示本次')
      void onboarding.refetch()
      void client.invalidateQueries({ queryKey: ['console-api-keys', scope.projectID] })
    },
  })

  const example = `export RELAYDOCK_API_KEY="<paste-the-one-time-secret>"\n\ncurl ${gatewayBase}/responses \\\n  -H "Authorization: Bearer $RELAYDOCK_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -H "Idempotency-Key: $(uuidgen)" \\\n  -d '{"model":"${alias || '<project-model-alias>'}","input":"Hello from ModelDock"}'`
  const facts = useMemo<OnboardingFacts | undefined>(() => onboarding.data ? deriveOnboardingFacts(onboarding.data.steps) : undefined, [onboarding.data])
  const regionReady = Boolean(organizationRegionReady && commercialTermsApproved(pricing.data?.commercial_terms) && pricing.data?.payment_region_supported === true && approvedPaymentChannelFee(pricing.data) && selectedProjectRoute?.model.pricing)
  const completed = facts ? Object.values(facts).filter(Boolean).length : 0
  const total = facts ? Object.keys(facts).length : 8

  return <div className="page-stack onboarding-page">
    <div className="page-header"><div><div className="eyebrow-row"><Sparkles size={14} />FIRST API REQUEST</div><h1>从账户到第一次成功调用</h1><p>每个完成状态都来自账户、支付、密钥、网关、用量或账本的服务端事实；刷新页面不会靠本地勾选伪造完成。</p></div><div className="header-actions"><Link to="/pricing"><Button>公开定价</Button></Link><Button onClick={() => onboarding.refetch()}><RefreshCw size={14} />刷新事实</Button></div></div>
    {onboarding.isLoading && <Panel><Skeleton rows={6} /></Panel>}
    {onboarding.isError && <ErrorState error={onboarding.error} onRetry={() => onboarding.refetch()} />}
    {facts && <><div className="onboarding-progress"><div><span>{completed} / {total} 个服务端阶段完成</span><strong>{completed === total ? '首次调用商业体验已完成' : `下一阶段：${(onboarding.data?.next_step || 'pending').replaceAll('_', ' ')}`}</strong></div><div className="onboarding-progress-track" role="progressbar" aria-valuemin={0} aria-valuemax={total} aria-valuenow={completed}><i style={{ width: `${completed / total * 100}%` }} /></div></div><ol className="onboarding-timeline" aria-label="Onboarding 进度"><TimelineStep complete={facts.registered} icon={<UserRoundCheck />} label="注册" /><TimelineStep complete={facts.email_verified} icon={<MailCheck />} label="邮箱验证" /><TimelineStep complete={facts.organization_created} icon={<Network />} label="组织" /><TimelineStep complete={facts.plan_selected} icon={<CircleDollarSign />} label="套餐" /><TimelineStep complete={facts.first_recharge} icon={<WalletCards />} label="充值" /><TimelineStep complete={facts.api_key_created} icon={<KeyRound />} label="API Key" /><TimelineStep complete={facts.first_api_call} icon={<Send />} label="首次调用" /><TimelineStep complete={facts.usage_visible} icon={<CheckCircle2 />} label="用量与扣费" /></ol></>}

    <Panel title="调用前确认：地区与真实价格" description={`当前查询 ${settings.region} / ${settings.currency}；切换公开站点页首选项可更改。`}>
      <div className="onboarding-pricing-check">
        {(pricing.isLoading || publicModels.isLoading) && <Skeleton rows={3} />}
        {(pricing.isError || publicModels.isError) && <div className="inline-warning">无法读取公开价格或模型地区状态。套餐选择保持不可用，请稍后重试或联系支持。</div>}
        {pricing.isSuccess && publicModels.isSuccess && <>
          {regionReady ? <div className="inline-note"><CheckCircle2 size={15} />组织地区、运行时支付渠道、已审核商业条款/渠道费以及项目 alias 的地区/价格映射均已确认。</div> : <div className="inline-warning">组织地区、项目 alias 的模型地区/价格映射、已审核商业条款，或该地区运行时支付渠道及费率证据不完整；自助付费套餐选择保持不可用。</div>}
          {selectedProjectRoute && <div className="inline-note"><Network size={15} />项目 alias <code>{alias}</code> 对应 {selectedProjectRoute.model.display_name}（{selectedProjectRoute.model.provider_name} / {selectedProjectRoute.model.provider_model_id}）：输入 {formatMoney(selectedProjectRoute.model.pricing!.input_token_price, selectedProjectRoute.model.pricing!.currency)}，输出 {formatMoney(selectedProjectRoute.model.pricing!.output_token_price, selectedProjectRoute.model.pricing!.currency)}，单位 {selectedProjectRoute.model.pricing!.unit}。</div>}
          {scope.projectID && projectModels.isSuccess && publicModels.isSuccess && !selectedProjectRoute && <div className="inline-warning">当前项目没有可与公开 Provider/model 证据匹配、且在 {settings.region} 有有效价格的 alias。Key 与首次调用示例保持不可用，请联系管理员配置项目模型路由。</div>}
          <div className="onboarding-commercial-summary"><span>赠送额度（条款披露）<strong>{pricing.data.commercial_terms ? isZeroDecimal(pricing.data.commercial_terms.bonus_credit_amount) ? '无已发布赠送额度' : formatMoney(pricing.data.commercial_terms.bonus_credit_amount, pricing.data.commercial_terms.currency) : '未发布'}</strong><small>实际可用额度仅以账户 promotion_credit 与钱包账本入账为准，非自动发放承诺。</small></span><span>订阅税费<strong>{pricing.data.commercial_terms?.subscription_tax_included === true ? '已含税' : pricing.data.commercial_terms?.subscription_tax_included === false ? '未含税' : '未披露/待确认'}</strong></span><span>Token 税费<strong>{pricing.data.commercial_terms?.token_tax_included === true ? '已含税' : pricing.data.commercial_terms?.token_tax_included === false ? '未含税' : '未披露/待确认'}</strong></span><span>退款<strong>{pricing.data.commercial_terms?.refund_summary || '未发布'}</strong></span><span>法律状态<strong>{pricing.data.commercial_terms?.legal_review_status === 'APPROVED' ? '已记录律师审核批准' : pricing.data.commercial_terms?.legal_review_status === 'PENDING' ? '待律师审核；购买已阻断' : '未披露/待确认'}</strong></span></div>
        </>}
      </div>
    </Panel>

    {!scope.organizations.length && !scope.loading && <Panel title="1. 创建组织" description={`组织是套餐、钱包、地区政策与账务的边界；新组织购买/客户地区将设置为当前公开选择 ${settings.region}。`}><Form className="form-grid onboarding-inline-form" onSubmit={() => createOrganization.mutateAsync()}><label><span>组织名称</span><input required value={organization.name} onChange={(event) => { const name = event.target.value; setOrganization((current) => ({ name, slug: current.slug || slugify(name) })) }} placeholder="Acme AI" /></label><label><span>URL slug</span><input required pattern="[a-z0-9]+(?:-[a-z0-9]+)*" value={organization.slug} onChange={(event) => setOrganization({ ...organization, slug: event.target.value.toLowerCase() })} placeholder="acme-ai" /></label>{createOrganization.isError && <div className="form-error full-span">{createOrganization.error instanceof Error ? createOrganization.error.message : '组织创建失败'}</div>}<SubmitButton pending={createOrganization.isPending} disabled={!organization.name.trim() || !organization.slug.trim()}>创建组织<ArrowRight size={14} /></SubmitButton></Form></Panel>}

    {scope.organization && !organizationRegionReady && <Panel title="2. 确认组织购买/客户地区" description="调用时会按组织地区重新执行 Provider 合同与地区准入；必须由用户显式确认，不能只依赖浏览器查询偏好。"><div className="onboarding-action-card"><Globe2 size={24} /><div><strong>{scope.organization.name}：{scope.organization.billing_region || '未设置'} → {settings.region}</strong><p>确认后会保留组织现有 metadata、Provider 允许/禁止列表、所需数据地区和最低毛利设置，仅覆盖 billing_region。</p></div><Button variant="primary" disabled={confirmOrganizationRegion.isPending} onClick={() => confirmOrganizationRegion.mutate()}>{confirmOrganizationRegion.isPending ? '保存中…' : `确认地区 ${settings.region}`}</Button></div>{confirmOrganizationRegion.isError && <div className="form-error">{confirmOrganizationRegion.error instanceof Error ? confirmOrganizationRegion.error.message : '组织地区更新失败'}</div>}</Panel>}

    {scope.organizationID && !scope.projects.length && !scope.loading && <Panel title="3. 创建项目" description="API Key、模型授权、用量和请求日志都按项目隔离。"><Form className="form-grid onboarding-inline-form" onSubmit={() => createProject.mutateAsync()}><label><span>项目名称</span><input required value={project.name} onChange={(event) => { const name = event.target.value; setProject((current) => ({ name, slug: current.slug || slugify(name) })) }} placeholder="Production API" /></label><label><span>URL slug</span><input required pattern="[a-z0-9]+(?:-[a-z0-9]+)*" value={project.slug} onChange={(event) => setProject({ ...project, slug: event.target.value.toLowerCase() })} placeholder="production-api" /></label>{createProject.isError && <div className="form-error full-span">{createProject.error instanceof Error ? createProject.error.message : '项目创建失败'}</div>}<SubmitButton pending={createProject.isPending} disabled={!project.name.trim() || !project.slug.trim()}>创建项目<ArrowRight size={14} /></SubmitButton></Form></Panel>}

    {scope.organizationID && facts && !facts.plan_selected && <Panel title="4. 选择套餐" description="订阅费不包含 Token 按量费；选择前请核对下面的真实服务端价格。"><div className="onboarding-plan-grid">{pricing.isSuccess && pricing.data.subscription_plans.map((plan) => <article key={plan.plan_version_id}><div><Badge>{plan.slug}</Badge><h3>{plan.name}</h3><p>{plan.description || '服务端未发布说明'}</p></div><strong>{formatMoney(plan.subscription_fee, plan.currency)} <small>/ {plan.billing_interval}</small></strong><span>Token：{plan.token_billing_mode} · 试用 {plan.trial_days} 天</span>{plan.contact_sales || plan.enterprise_contract ? <Link className="button button-default button-md" to="/enterprise">联系企业服务<ArrowRight size={14} /></Link> : <Button variant="primary" disabled={!regionReady || choosePlan.isPending} onClick={() => choosePlan.mutate(plan.plan_version_id)}>选择套餐</Button>}</article>)}{pricing.isSuccess && !pricing.data.subscription_plans.length && <EmptyState title="当前地区没有已发布套餐" />}</div>{choosePlan.isError && <div className="form-error">{choosePlan.error instanceof Error ? choosePlan.error.message : '套餐选择失败'}</div>}<div className="onboarding-panel-links"><Link to="/pricing">查看完整 Token、渠道费、税与退款披露<ArrowRight size={14} /></Link><Link to="/console/subscription">打开完整订阅管理<ArrowRight size={14} /></Link></div></Panel>}

    {facts?.plan_selected && !facts.first_recharge && <Panel title="5. 完成首次充值" description="浏览器只能创建支付订单；只有服务端验证支付并写入钱包账本后，向导才判定完成。"><div className="onboarding-action-card"><WalletCards size={24} /><div><strong>创建并完成支付订单</strong><p>充值页会展示当前启用的支付渠道、合同状态和订单状态。测试或人工渠道不会被描述为真实自动入账。</p></div>{regionReady ? <Link className="button button-primary button-md" to="/console/recharge">前往充值<ArrowRight size={14} /></Link> : <Button disabled>先完成地区、价格与支付确认</Button>}</div></Panel>}

    {facts?.first_recharge && scope.projectID && <Panel title={facts.api_key_created ? '6. 创建替代 API Key（如需）' : '6. 创建 API Key'} description="完整 rdk_* 密钥只在成功响应中显示一次；向导只授权第一个已映射到当前地区公开价格的项目模型，遵循最小权限。"><Form className="form-grid onboarding-inline-form" onSubmit={() => createKey.mutateAsync()}>{facts.api_key_created && !secret && <div className="inline-warning full-span">该项目已有 Key，但完整 secret 无法再次读取。如果尚未安全保存，请在此创建一个新的最小权限 Key，并在确认可用后到 API Keys 页撤销遗失的 Key。</div>}<label className="full-span"><span>Key 名称</span><input required value={keyName} onChange={(event) => setKeyName(event.target.value)} /></label>{!organizationRegionReady && <div className="inline-warning full-span">组织购买/客户地区尚未与 {settings.region} 一致，Key 创建已禁用。</div>}{projectModels.isLoading && <div className="inline-note full-span">正在读取项目模型授权…</div>}{projectModels.isError && <div className="form-error full-span">无法读取项目模型授权；Key 创建已禁用，请联系管理员。</div>}{projectModels.isSuccess && !alias && <div className="inline-warning full-span">当前项目没有可与公开 Provider/model、地区和价格证据匹配的模型别名。请先由管理员配置项目模型授权，向导不会创建可访问全部模型的 Key。</div>}{alias && <div className="inline-note full-span">最小权限模型：<code>{alias}</code> · {selectedProjectRoute?.model.display_name}</div>}{createKey.isError && <div className="form-error full-span">{createKey.error instanceof Error ? createKey.error.message : 'API Key 创建失败'}</div>}<SubmitButton pending={createKey.isPending} disabled={!organizationRegionReady || !keyName.trim() || !alias}>{facts.api_key_created ? '创建替代 Key' : '创建项目 Key'}<KeyRound size={14} /></SubmitButton></Form></Panel>}

    {secret && <Panel title="只显示一次：保存 API Key" description="关闭或刷新后无法再次查看完整值；不要把密钥放入源码、日志、浏览器应用或聊天。"><div className="onboarding-secret"><code>{secret}</code><Button onClick={() => { void navigator.clipboard.writeText(secret); toast('API Key 已复制') }}><Copy size={14} />复制密钥</Button></div><div className="inline-warning">请先保存到密钥管理器，再继续复制不含真实密钥的调用示例。</div><Button variant="ghost" onClick={() => setSecret('')}>我已安全保存，隐藏密钥</Button></Panel>}

    {facts?.api_key_created && <Panel title="7. 复制示例并发出首次请求" description="示例仅引用 RELAYDOCK_API_KEY 环境变量，不会把真实密钥写入命令历史模板。"><div className="onboarding-code"><header><span>{alias ? `项目模型：${alias}` : '项目尚无可用模型别名'}</span><Button size="sm" disabled={!alias || !organizationRegionReady} onClick={() => { void navigator.clipboard.writeText(example); setExampleCopied(true); toast('调用示例已复制') }}><Copy size={13} />{exampleCopied ? '已复制' : '复制示例'}</Button></header><pre><code>{example}</code></pre></div>{projectModels.isLoading && <Skeleton rows={2} />}{projectModels.isError && <div className="inline-warning">无法读取项目模型，示例保持不可执行。请检查项目模型授权或联系管理员。</div>}{projectModels.isSuccess && !alias && <div className="inline-warning">当前项目没有可与公开价格和地区证据匹配的授权模型别名，不能发起第一次调用。</div>}{!organizationRegionReady && <div className="inline-warning">组织地区与当前查询地区不一致，示例保持不可执行。</div>}<div className="onboarding-panel-links">{alias && organizationRegionReady ? <Link to="/console/playground">在 Playground 发出请求<ArrowRight size={14} /></Link> : <span>Playground 入口等待模型/地区确认</span>}<Link to="/docs#ordinary-request">阅读普通请求与 SSE 文档<ArrowRight size={14} /></Link></div></Panel>}

    {facts?.api_key_created && <Panel title="8. 验证首次调用、用量与扣费" description="刷新事实后，只有网关请求和账务/用量证据存在时才显示完成。"><div className="onboarding-verification-grid"><Verification label="首次 API 调用" complete={facts.first_api_call} detail={facts.first_api_call ? '网关已记录首次调用事件。' : '尚未观察到成功的首次调用。'} link="/console/logs" /><Verification label="用量与扣费可见" complete={facts.usage_visible} detail={facts.usage_visible ? '用量或计费证据已可查询。' : '等待 Token 结算与用量记录。'} link="/console/billing" /></div><div className="onboarding-panel-links"><Button size="sm" onClick={() => onboarding.refetch()}><RefreshCw size={13} />刷新服务端事实</Button><Link to="/console/usage">查看用量<ArrowRight size={14} /></Link><Link to="/console/billing">查看扣费与账本<ArrowRight size={14} /></Link></div></Panel>}
  </div>
}

function TimelineStep({ complete, icon, label }: { complete: boolean; icon: React.ReactNode; label: string }) {
  return <li className={complete ? 'complete' : undefined}><span>{complete ? <Check size={13} /> : icon}</span><strong>{label}</strong></li>
}

function Verification({ label, complete, detail, link }: { label: string; complete: boolean; detail: string; link: string }) {
  return <article className={complete ? 'complete' : undefined}>{complete ? <CheckCircle2 size={21} /> : <Circle size={21} />}<div><strong>{label}</strong><p>{detail}</p><Link to={link}>查看证据<ArrowRight size={13} /></Link></div></article>
}
