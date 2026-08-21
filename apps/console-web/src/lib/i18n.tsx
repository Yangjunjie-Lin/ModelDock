import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export type Language = 'en' | 'zh'
const storageKey = 'rd-language'
const zh: Record<string, string> = {
  'Build': '构建', 'Developer': '开发者', 'Overview': '概览', 'API Keys': 'API 密钥', 'Models': '模型', 'Usage': '用量', 'Logs': '日志', 'Playground': '调试台', 'Documentation': '文档', 'Workspace': '工作区', 'Console': '控制台',
  'Cloud console': '云控制台', 'Organization': '组织', 'Project': '项目', 'Select organization': '选择组织', 'Select project': '选择项目', 'RelayDock user': 'RelayDock 用户', 'Console access': '控制台访问',
  'Search console': '搜索控制台', 'Search pages…': '搜索页面…', 'Open': '打开', 'Open navigation': '打开导航', 'Close navigation': '关闭导航', 'Close': '关闭', 'Toggle sidebar': '切换侧栏', 'Toggle theme': '切换主题', 'Language': '语言', 'Switch to Chinese': '切换到中文', 'Switch to English': '切换到英文',
  'Welcome back': '欢迎回来', 'Sign in to your RelayDock workspace.': '登录 RelayDock 工作区。', 'Email address': '邮箱地址', 'Password': '密码', 'Enter your password': '输入密码', 'Continue': '继续', 'Open demo workspace': '打开演示工作区', 'Need access? Contact your RelayDock administrator.': '如需访问权限，请联系 RelayDock 管理员。',
  'DEVELOPER CONSOLE': '开发者控制台', 'A clean path from code to models.': '从代码直达模型。', 'Create scoped keys, use stable aliases, and understand every request from a focused developer workspace.': '创建限定范围的密钥、使用稳定别名，并在专注的开发者工作区中了解每次请求。', 'OpenAI-compatible SDKs': '兼容 OpenAI 的 SDK', 'Scoped RelayDock keys': '限定范围的 RelayDock 密钥', 'Sanitized request logs': '脱敏请求日志',
  'Create API key': '创建 API 密钥', 'Create key': '创建密钥', 'Delete': '删除', 'Rotate': '轮换', 'Finalize': '完成', 'Copy': '复制', 'Copied': '已复制', 'Name': '名称', 'Status': '状态', 'Created': '创建时间', 'Last used': '最近使用', 'Never': '从未',
  'Requests': '请求数', 'Tokens': 'Token 数', 'Cost': '费用', 'Latency': '延迟', 'Error rate': '错误率', 'Model': '模型', 'Route': '路由', 'Refresh': '刷新', 'Download CSV': '下载 CSV', 'No data': '暂无数据', 'Previous': '上一页', 'Next': '下一页', 'Cancel': '取消', 'Save': '保存', 'Send request': '发送请求', 'Response': '响应',
}
Object.assign(zh, {
  'GATEWAY READY': '网关就绪', 'GET STARTED': '开始使用', 'RELAYDOCK API': 'RELAYDOCK API', 'API REFERENCE': 'API 参考', 'Build with one endpoint.': '通过一个端点完成构建。', 'Use stable model aliases, RelayDock API keys, and OpenAI-compatible SDKs.': '使用稳定的模型别名、RelayDock API 密钥和兼容 OpenAI 的 SDK。',
  'Daily requests': '每日请求', 'Recent requests': '最近请求', 'Request activity': '请求活动', 'Workspace consumption for the current cycle': '当前周期工作区用量', 'Resolved requests during the last 24 hours': '过去 24 小时已处理请求', 'Your first API request will appear here.': '首个 API 请求将显示在这里。', 'View all': '查看全部',
  'Project-granted model aliases': '项目授权的模型别名', 'Model aliases': '模型别名', 'Model alias': '模型别名', 'Project grants': '项目授权', 'No models available': '暂无可用模型', 'Your administrator has not granted matching model aliases to this project.': '管理员尚未向此项目授权匹配的模型别名。', 'Search models and aliases…': '搜索模型和别名…',
  'Manage API keys': '管理 API 密钥', 'Create and rotate project-scoped RelayDock keys. Full secrets are shown exactly once.': '创建和轮换项目范围的 RelayDock 密钥。完整密钥只显示一次。', 'No API keys': '暂无 API 密钥', 'Create a project key to authenticate your first gateway request.': '创建项目密钥以验证首个网关请求。', 'Create project API key': '创建项目 API 密钥', 'Key name *': '密钥名称 *', 'Environment': '环境', 'Allowed models (empty uses all project grants)': '允许的模型（留空使用全部项目授权）', 'Rotate API key': '轮换 API 密钥', 'Finalize key rotation': '完成密钥轮换', 'Revoke key': '撤销密钥', 'ONE-TIME SECRET': '一次性密钥', 'I have saved this key': '我已保存此密钥', 'Store this key in a secure secret manager.': '请将此密钥保存到安全的密钥管理器。',
  'Understand project consumption, reliability, and estimated cost.': '了解项目用量、可靠性与估算费用。', 'Usage by model': '按模型统计用量', 'Request volume for the selected period': '所选时段的请求量', 'Monthly limits': '月度限额', 'Export project usage': '导出项目用量', 'From (UTC) *': '起始时间（UTC）*', 'To (UTC) *': '结束时间（UTC）*', 'No usage in this period': '此时段暂无用量', 'Estimated cost is calculated using RelayDock configured pricing. It is not a provider invoice.': '估算费用按 RelayDock 配置的价格计算，并非提供商账单。',
  'Sanitized request metadata': '脱敏请求元数据', 'Inspect sanitized metadata for the selected project. Prompt and response content are not displayed.': '检查所选项目的脱敏元数据，不显示提示或响应内容。', 'Search request ID…': '搜索请求 ID…', 'All statuses': '全部状态', 'No request logs': '暂无请求日志', 'Request details': '请求详情', 'Request metadata is isolated by project.': '请求元数据按项目隔离。', 'Success': '成功', 'Rate limited': '已限流', 'Errors': '错误',
  'Test RelayDock model aliases with an OpenAI-compatible request.': '通过兼容 OpenAI 的请求测试 RelayDock 模型别名。', 'Configure the request and press Run.': '配置请求后点击运行。', 'Run request': '运行请求', 'Stop': '停止', 'Reset output': '重置输出', 'No response yet': '尚无响应', 'Waiting for the first response chunk…': '正在等待首个响应分块…', 'Stream response': '流式响应', 'System instructions': '系统指令',
  'Developer documentation': '开发者文档', 'Quick start': '快速开始', 'Quickstart': '快速入门', 'Authentication': '身份验证', 'Responses API': 'Responses API', 'Embeddings': '嵌入', 'Model guide': '模型指南', 'Read the docs': '阅读文档', 'Open Playground': '打开调试台', 'Try it in the Playground': '在调试台中试用',
  'Unable to load data': '无法加载数据', 'Clear search': '清除搜索', 'Page not found': '页面不存在', 'Return to overview': '返回概览', 'Select a project': '选择项目', 'Request logs': '请求日志', 'Requests per minute': '每分钟请求数', 'Tokens per minute': '每分钟 Token 数', 'Estimated cost': '估算费用',
})

Object.assign(zh, {
  'Billing': '账务中心', 'Recharge': '充值', 'Subscription': '订阅', 'Account': '账户',
  'Trace every payment and Token charge, review monthly statements, and submit refund or invoice applications.': '追踪每笔付款与 Token 扣费，查看月度账单，并提交退款或发票申请。',
  'Month': '月份', 'Recharge history': '充值历史', 'Token usage': 'Token 消耗', 'Subscription orders': '订阅订单', 'Monthly statements': '月度账单', 'Monthly statement': '月度账单', 'Refunds': '退款', 'Invoices': '发票', 'Downloading…': '下载中…',
  'Total available': '可用总额', 'Cash balance': '现金余额', 'Bonus balance': '赠送余额', 'Credit available': '可用信用额度', 'Reserved': '预留金额', 'Pending request reservations': '待结算请求预留', 'Promotional; not cash-refundable': '赠送额度，不可作为现金退款',
  'A credited order is linked to its immutable wallet transaction and ledger journal.': '已入账订单关联不可变的钱包交易与账本日记账。', 'No recharge history': '暂无充值历史', 'Completed recharge orders will appear here.': '已完成的充值订单会显示在这里。', 'Wallet trace': '钱包追踪', 'Paid / created': '支付 / 创建时间',
  'Token consumption details': 'Token 消耗明细', 'Every charge shows the customer debit, wallet evidence, and the Provider request selected for that API call.': '每笔扣费均展示用户扣款、钱包凭证及该 API 调用对应的 Provider 请求。', 'No Token usage': '暂无 Token 消耗', 'No billable API usage was recorded for the selected month.': '所选月份暂无可计费用量。', 'User charge': '用户扣费', 'Provider request': 'Provider 请求',
  'Subscription fees are separate from metered Token charges and retain their own payment and ledger references.': '订阅费与按量 Token 扣费分开，并保留独立支付和账本引用。', 'No subscription orders': '暂无订阅订单', 'Free and trial periods may not create a paid order.': '免费版和试用期可能不会生成付费订单。', 'Subscription fee': '订阅费', 'Service period': '服务周期', 'Payment trace': '支付追踪', 'Ledger journal': '账本日记账',
  'Statements summarize settled recharge, usage, subscription, refund, Provider cost, and balance activity by currency.': '月账单按币种汇总已结算充值、用量、订阅、退款、Provider 成本与余额变动。', 'No monthly statement': '暂无月度账单', 'No settled financial activity exists for the selected month.': '所选月份暂无已结算财务活动。', 'Opening': '期初余额', 'Recharge / subscription': '充值 / 订阅', 'Usage / bonus': '用量 / 赠送', 'Closing': '期末余额',
  'Refund applications': '退款申请', 'Request refund': '申请退款', 'Eligibility is assessed server-side across unused cash, consumed service, bonus, subscription fees, and irreversible Provider costs.': '服务端将按未使用现金、已使用服务、赠送额度、订阅费及不可撤销 Provider 成本评估资格。', 'No refund applications': '暂无退款申请', 'Refund requests and their review status will appear here.': '退款申请及审核状态会显示在这里。', 'Application': '申请单', 'Requested': '申请金额', 'Eligibility assessment': '资格评估', 'Submitted': '提交时间',
  'Invoice applications': '发票申请', 'Request invoice': '申请发票', 'ModelDock validates and exports applications for finance processing. No tax-system integration or automatic issuance is implied.': 'ModelDock 仅校验并导出申请供财务处理，不代表已接入税务系统或可自动开票。', 'Invoice status represents the application workflow only. Approved or exported does not mean a tax invoice was automatically issued.': '发票状态仅表示申请流程；已批准或已导出不代表税票已自动开具。', 'No invoice applications': '暂无发票申请', 'Submitted invoice requests and their processing status will appear here.': '已提交的发票申请及处理状态会显示在这里。', 'Amount': '金额', 'Eligible period': '可开票期间',
  'Request a refund': '提交退款申请', 'Finance reviews the request against immutable payment, wallet, usage, subscription, and Provider-cost evidence.': '财务将依据不可变的支付、钱包、用量、订阅及 Provider 成本凭证审核。', 'Submit application': '提交申请', 'Refund category': '退款类别', 'Unused cash from recharge': '充值未使用现金', 'Source order': '来源订单', 'Select an eligible order': '选择符合条件的订单', 'Requested amount': '申请退款金额', 'Reason': '原因', 'Explain why this payment or subscription fee should be reviewed.': '说明为何需要审核这笔付款或订阅费。',
  'Request an invoice': '提交发票申请', 'The server validates the amount against settled recharge and paid subscription evidence in the selected period.': '服务端将按所选期间已结算充值与已支付订阅凭证校验金额。', 'Invoice title': '发票抬头', 'Tax identifier (optional)': '纳税人识别号（可选）', 'Currency': '币种', 'Period start': '期间开始', 'Period end': '期间结束', 'This submits an application for manual finance processing and export. ModelDock does not claim to issue a tax invoice automatically.': '此操作仅提交申请供财务人工处理和导出；ModelDock 不声称能够自动开具税票。',
  'Requests today': '今日请求',
  'Current UTC day': '当前 UTC 日期',
  'Total requests': '请求总数',
  'Recorded workspace traffic': '已记录的工作区流量',
  '24 Hours': '24 小时',
  '24 hours': '24 小时',
  '24h ago': '24 小时前',
  '12h ago': '12 小时前',
  'No workspace limit configured': '未配置工作区限额',
  'Output will appear here': '输出将显示在这里',
  'including server-sent event streaming.': '包括服务器发送事件（SSE）流式传输。',
  ', including server-sent event streaming.': '，包括服务器发送事件（SSE）流式传输。',
})
Object.assign(zh, {
  '30 days': '30 天', '7 days': '7 天', 'Today': '今天', 'Allowed models': '允许的模型', 'Cached input': '缓存输入', 'Gateway and upstream errors': '网关与上游错误', 'Input tokens': '输入 Token', 'Input, cached, and output': '输入、缓存输入和输出', 'Output tokens': '输出 Token', 'Rate limit': '速率限制', 'RelayDock configured pricing': 'RelayDock 配置价格', 'Request ID': '请求 ID', 'Save the rotated API key': '保存轮换后的 API 密钥', 'Save your API key': '保存 API 密钥', 'The old key remains valid only for the configured grace period.': '旧密钥仅在配置的宽限期内有效。', 'This project key is shown exactly once.': '此项目密钥仅显示一次。', 'Time': '时间', 'Total tokens': 'Token 总数',
})
Object.assign(zh, {
  'API keys': 'API 密钥', 'Aliases such as': '例如以下别名', 'All capabilities': '全部能力', 'Audio': '音频', 'Both old and new secrets authenticate during the grace period. Finalize once every workload uses the new secret.': '宽限期内新旧密钥均可认证；所有工作负载切换后请完成轮换。', 'CONSOLE': '控制台', 'Chat Completions': '聊天补全', 'Choose an organization and project from the sidebar to view isolated usage and keys.': '从侧栏选择组织和项目，以查看隔离的用量与密钥。', 'Client-side preview': '客户端预览',
  'Confirm that all applications have switched to the newest secret. This action cannot restore an old secret.': '请确认所有应用均已切换到最新密钥，此操作无法恢复旧密钥。', 'Connect an OpenAI-compatible SDK to RelayDock, choose a stable model alias, and start sending requests.': '将兼容 OpenAI 的 SDK 连接到 RelayDock，选择稳定的模型别名并开始发送请求。', 'Context': '上下文', 'Do not expose this key in browser code, source control, logs, or chat messages.': '请勿在浏览器代码、源代码管理、日志或聊天消息中暴露此密钥。', 'Embedding': '嵌入', 'Endpoint': '端点', 'Error': '错误',
  'Errors follow the OpenAI-compatible': '错误遵循 OpenAI 兼容的', 'Estimated costs use pricing configured by your RelayDock administrator and may differ from a provider invoice.': '估算费用采用 RelayDock 管理员配置的价格，可能与提供商账单不同。', 'Existing OpenAI-compatible integrations can use': '现有兼容 OpenAI 的集成可以使用', 'Expires': '到期时间', 'Export CSV': '导出 CSV', 'Grace period (minutes) *': '宽限期（分钟）*', 'Image': '图像', 'Input': '输入', 'Jump to a RelayDock console page.': '跳转到 RelayDock 控制台页面。', 'Keys are issued and listed only within the selected project.': '密钥仅在所选项目中签发和列出。', 'Live': '实时', 'Loading': '加载中',
  'Model aliases are isolated by project route grants.': '模型别名按项目路由授权隔离。', 'Never place an upstream provider credential in application requests. RelayDock selects an authorized provider credential internally.': '绝不要在应用请求中放入上游提供商凭据；RelayDock 会在内部选择已授权凭据。', 'Next page': '下一页', 'No model usage': '暂无模型用量', 'No recent requests': '暂无最近请求', 'No request activity': '暂无请求活动', 'Now': '当前', 'Only RelayDock request metadata is available in Console. Upstream credential identifiers and provider request IDs are not exposed.': '控制台仅提供 RelayDock 请求元数据，不暴露上游凭据标识或提供商请求 ID。', 'OpenAI Python SDK': 'OpenAI Python SDK',
  'Previous page': '上一页', 'Production application': '生产应用', 'Reasoning': '推理', 'RelayDock API key': 'RelayDock API 密钥', 'RelayDock Cloud Console · Authorized client access': 'RelayDock 云控制台 · 已授权客户端访问', 'RelayDock accepts 30 seconds to 24 hours; Console uses whole minutes.': 'RelayDock 接受 30 秒至 24 小时；控制台使用整分钟。', 'RelayDock exposes an OpenAI-compatible base URL. Choose your SDK, provide a RelayDock API key through an environment variable, and use an alias enabled for your workspace.': 'RelayDock 提供兼容 OpenAI 的基础地址。请选择 SDK，通过环境变量提供 RelayDock API 密钥，并使用工作区启用的别名。',
  'RelayDock resolves the current provider model.': 'RelayDock 解析当前提供商模型。', 'Render server-sent events as they arrive': '实时渲染服务器发送事件', 'Requests matching the current project and filters will appear here.': '匹配当前项目和筛选条件的请求将显示在这里。', 'Responses': '响应', 'Retry': '重试', 'Revoke all prior grace versions': '撤销所有先前宽限版本', 'Revoke every older grace version immediately. The newest active secret remains valid.': '立即撤销所有旧宽限版本，最新的活动密钥仍然有效。', 'Rotate key': '轮换密钥', 'Rotation preserves policy and accounting identity. Finalization immediately revokes all older grace versions.': '轮换会保留策略和计费标识；完成后立即撤销所有旧宽限版本。',
  'Search API keys…': '搜索 API 密钥…', 'Select': '选择', 'Send a RelayDock API key as a Bearer token. Keep it on the server and load it from a secret manager or environment variable.': '将 RelayDock API 密钥作为 Bearer token 发送；请保存在服务器端，并从密钥管理器或环境变量加载。', 'Send embedding workloads to': '将嵌入工作负载发送到', 'Test': '测试', 'Text': '文本', 'The API key exists only in component memory and is cleared when this page closes. Console does not persist it.': 'API 密钥仅存在于组件内存中，页面关闭时会清除；控制台不会持久保存。',
  'The export contains request IDs, aliases, status, token counts, estimated cost, and latency. It excludes prompts, responses, cookies, authorization headers, and secrets.': '导出内容包含请求 ID、别名、状态、Token 数、估算费用和延迟，不含提示、响应、Cookie、授权头或密钥。', 'The requested Console page does not exist.': '请求的控制台页面不存在。', 'The scheduler selects an eligible credential.': '调度器选择符合条件的凭据。', 'Tools': '工具', 'Type': '类型', 'Usage and CSV exports are isolated by project.': '用量和 CSV 导出按项目隔离。', 'Usage and request metadata are recorded.': '系统会记录用量与请求元数据。', 'Use': '使用', 'Use downstream RelayDock keys': '使用下游 RelayDock 密钥', 'Versioned keys are hashed at rest': '版本化密钥以哈希形式静态保存', 'Vision': '视觉', 'Your app sends the configured alias.': '应用发送已配置的别名。',
  'envelope where possible. Use the RelayDock request ID when contacting an administrator.': '封装格式。联系管理员时请提供 RelayDock 请求 ID。', 'for new text and tool-enabled integrations. Streaming is supported with': '用于新的文本和工具集成。流式响应支持', 'keep application configuration stable while administrators manage upstream model routes.': '让管理员管理上游模型路由时保持应用配置稳定。', 'with an alias enabled for your workspace.': '并使用工作区已启用的别名。',
})

const Context = createContext<{ language: Language; setLanguage: (language: Language) => void }>({ language: 'en', setLanguage: () => undefined })
const textOriginals = new WeakMap<Node, string>()
const attributeOriginals = new WeakMap<Element, Map<string, string>>()
const attributes = ['aria-label', 'placeholder', 'title']
function translate(value: string) {
  if (zh[value]) return zh[value]
  const status = ({ active: '启用', disabled: '禁用', archived: '已归档', healthy: '健康', unhealthy: '不健康', unknown: '未知', available: '可用', success: '成功', warning: '警告', error: '错误', pending: '等待中', revoked: '已撤销', enabled: '已启用', yes: '是', no: '否' } as Record<string, string>)[value.toLowerCase()]
  if (status) return status
  let match = value.match(/^(\d+)–(\d+) of (\d+)$/)
  if (match) return `${match[1]}–${match[2]} / 共 ${match[3]} 条`
  match = value.match(/^(\d+) results?$/)
  if (match) return `共 ${match[1]} 条`
  match = value.match(/^Page (\d+) of (\d+)$/)
  if (match) return `第 ${match[1]} / ${match[2]} 页`
  match = value.match(/^(.+) \/ No limit$/)
  if (match) return `${match[1]} / 无限制`
  match = value.match(/^(\d+) active keys$/)
  if (match) return `${match[1]} 个活动密钥`
  match = value.match(/^(\d+) models$/)
  if (match) return `${match[1]} 个模型`
  match = value.match(/^(\d+(?:\.\d+)?)% used$/)
  if (match) return `已使用 ${match[1]}%`
  match = value.match(/^Explore stable aliases granted to (.+)\.$/)
  if (match) return `查看已授权给 ${match[1]} 的稳定别名。`
  return value
}

function text(node: Node, language: Language) {
  if (node.parentElement?.closest('script, style, code, pre, [data-no-translate]')) return
  if (language === 'en') { const original = textOriginals.get(node); if (original !== undefined && node.nodeValue !== original) node.nodeValue = original; return }
  const current = node.nodeValue || ''; const trimmed = current.trim(); if (!trimmed) return; const value = translate(trimmed); if (value === trimmed) return
  textOriginals.set(node, current); node.nodeValue = current.replace(trimmed, value)
}
function element(node: Element, language: Language) {
  let originals = attributeOriginals.get(node)
  for (const name of attributes) {
    if (language === 'en') { const original = originals?.get(name); if (original !== undefined) node.setAttribute(name, original); continue }
    const current = node.getAttribute(name); if (!current) continue; const value = translate(current); if (value === current) continue
    if (!originals) { originals = new Map(); attributeOriginals.set(node, originals) }; originals.set(name, current); node.setAttribute(name, value)
  }
}
function tree(root: Node, language: Language) {
  if (root.nodeType === Node.TEXT_NODE) text(root, language); if (root.nodeType === Node.ELEMENT_NODE) element(root as Element, language)
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT); let node: Node | null
  while ((node = walker.nextNode())) {
    if (node.nodeType === Node.TEXT_NODE) text(node, language)
    else element(node as Element, language)
  }
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, setLanguage] = useState<Language>(() => { const saved = localStorage.getItem(storageKey); return saved === 'en' || saved === 'zh' ? saved : 'zh' })
  useEffect(() => {
    localStorage.setItem(storageKey, language); document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en'; tree(document.body, language)
    const observer = new MutationObserver((mutations) => mutations.forEach((mutation) => { if (mutation.type === 'characterData') text(mutation.target, language); if (mutation.type === 'attributes') element(mutation.target as Element, language); mutation.addedNodes.forEach((node) => tree(node, language)) }))
    observer.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: attributes }); return () => observer.disconnect()
  }, [language])
  return <Context.Provider value={useMemo(() => ({ language, setLanguage }), [language])}>{window.location.pathname === '/login' && <div className="login-language"><LanguageToggle /></div>}{children}</Context.Provider>
}
export function LanguageToggle({ compact = false }: { compact?: boolean }) {
  const { language, setLanguage } = useContext(Context)
  return <div className={`language-toggle ${compact ? 'compact' : ''}`} role="group" aria-label="Language"><button type="button" className={language === 'en' ? 'active' : ''} onClick={() => setLanguage('en')} title="Switch to English">EN</button><button type="button" className={language === 'zh' ? 'active' : ''} onClick={() => setLanguage('zh')} title="Switch to Chinese">中文</button></div>
}
export function currentLocale() { return localStorage.getItem(storageKey) === 'en' ? 'en-US' : 'zh-CN' }
