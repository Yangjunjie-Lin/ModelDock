import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export type Language = 'en' | 'zh'

const storageKey = 'rd-language'
const zh: Record<string, string> = {
  'Workspace': '工作区', 'Dashboard': '仪表盘', 'Organizations': '组织', 'Projects': '项目', 'Providers': '提供商', 'Credential Pool': '凭据池', 'Credential pool': '凭据池', 'Groups': '分组',
  'Gateway': '网关', 'Models': '模型', 'Routes': '路由', 'API Keys': 'API 密钥', 'Users': '用户', 'Observability': '可观测性', 'Usage': '用量', 'Request Logs': '请求日志', 'Audit Logs': '审计日志', 'Alerts': '告警', 'Webhooks': 'Webhook', 'Settings': '设置',
  'Control plane': '控制平面', 'Administrator': '管理员', 'Admin workspace': '管理工作区', 'Admin': '管理', 'Search': '搜索', 'Search console': '搜索控制台', 'Search pages…': '搜索页面…', 'Type a page name…': '输入页面名称…', 'Go to': '跳转到', 'Open': '打开', 'No matching pages.': '没有匹配的页面。',
  'Open navigation': '打开导航', 'Close navigation': '关闭导航', 'Close': '关闭', 'Toggle sidebar': '切换侧栏', 'Toggle theme': '切换主题', 'Switch to Chinese': '切换到中文', 'Switch to English': '切换到英文', 'Language': '语言',
  'AUTHORIZED CREDENTIALS': '已授权凭据', 'Health, load, and scheduling controls for administrator-supplied provider credentials.': '管理由管理员提供的凭据健康状态、负载与调度。', 'Export metadata': '导出元数据', 'Import JSON': '导入 JSON', 'Open official dashboard': '打开官方控制台', 'Add credential': '添加凭据',
  'Visible credentials': '可见凭据', 'Healthy': '健康', 'Active': '启用', 'Rate limited': '已限流', 'Max concurrency': '最大并发', 'Search name, project, or tag…': '搜索名称、项目或标签…', 'All statuses': '全部状态', 'All groups': '全部分组', 'Cooldown': '冷却中', 'Auth failed': '授权失败', 'Disabled': '已禁用', 'Card view': '卡片视图', 'Table view': '表格视图',
  'Enable': '启用', 'Disable': '禁用', 'Health check': '健康检查', 'Move group': '移动分组', 'Delete': '删除', 'Clear': '清除', 'No credentials configured': '尚未配置凭据', 'Add an authorized provider API credential to enable routing.': '添加已授权的提供商 API 凭据以启用路由。', 'Add your first credential': '添加首个凭据',
  'Add OpenAI credential': '添加 OpenAI 凭据', 'Cancel': '取消', 'Save disabled': '保存为禁用', 'Validate & save': '验证并保存', 'Name *': '名称 *', 'Provider ID *': '提供商 ID *', 'Credential group': '凭据分组', 'OpenAI API key *': 'OpenAI API 密钥 *', 'Organization ID': '组织 ID', 'Project ID': '项目 ID', 'Scheduler tags': '调度标签', 'Weight': '权重', 'Priority': '优先级', 'Optional': '可选', 'Optional group ID': '可选分组 ID',
  'Enter a newly issued official API key': '输入新签发的官方 API 密钥', 'The secret is encrypted at rest and cannot be retrieved after saving.': '密钥静态加密保存，保存后无法取回。', 'Import authorized credentials': '导入已授权凭据', 'Validate & import': '验证并导入', 'Credential JSON *': '凭据 JSON *', 'Move selected credentials': '移动选中的凭据', 'Move credentials': '移动凭据', 'Target group ID *': '目标分组 ID *', 'Credential group ID': '凭据分组 ID', 'Delete selected credentials': '删除选中的凭据', 'Credential details': '凭据详情', 'Test connection': '测试连接',
  'Credential': '凭据', 'Status': '状态', 'Group': '分组', 'Load': '负载', 'Error': '错误', 'Last request': '最近请求', 'Secret': '密钥', 'Concurrency': '并发', 'Recent RPM': '近期 RPM', 'Recent TPM': '近期 TPM', 'Error rate': '错误率', 'Test': '测试', 'Details': '详情', 'Provider': '提供商', 'Project': '项目', 'Health': '健康状态', 'Last success': '最近成功', 'Last failure': '最近失败', 'Cooldown until': '冷却截止', 'Save tags': '保存标签', 'Saving…': '保存中…', 'Loading…': '加载中…',
  'COCKPIT AUTHORIZED ACCOUNTS': 'COCKPIT 已授权账号', 'Cockpit account quota': 'Cockpit 账号额度', 'Read-only quota and authorization status from the local Cockpit sidecar. No OAuth tokens, cookies, or passwords enter RelayDock.': '从本机 Cockpit sidecar 只读获取额度和授权状态。OAuth token、Cookie 和密码不会进入 RelayDock。', 'Refresh snapshot': '刷新快照', 'Test sidecar': '测试 sidecar', 'Snapshot unavailable': '快照不可用', 'Run scripts/sync-cockpit.ps1 on the host to create a sanitized account snapshot.': '请在宿主机运行 scripts/sync-cockpit.ps1，生成脱敏账号快照。',
  'Remaining quota': '剩余额度', 'Primary window': '主额度窗口', 'Secondary window': '次额度窗口', 'Resets': '重置时间', 'Subscription expires': '订阅到期', 'Last updated': '最后更新', 'Ready': '可用', 'Quota exhausted': '额度已用尽', 'Live verification passed': '实时验证通过', 'Live test is not configured in the RelayDock container. The latest host-side test is shown below.': 'RelayDock 容器尚未配置实时测试；下方显示最近一次宿主机测试结果。', 'Security boundary': '安全边界', 'Only masked email, plan, status, quota percentage, and timestamps are stored in this snapshot.': '快照仅保存脱敏邮箱、套餐、状态、额度百分比和时间戳。',
  'Sign in': '登录', 'Email address': '邮箱地址', 'Password': '密码', 'Continue': '继续', 'Signing in…': '登录中…', 'Welcome back': '欢迎回来', 'Enter your password': '输入密码', 'Open demo workspace': '打开演示工作区',
  'authorized accounts': '已授权账号', 'ready': '可用', 'Snapshot': '快照', 'Live verification passed ·': '实时验证通过 ·', 'Cockpit quota snapshot refreshed': 'Cockpit 额度快照已刷新',
  'Back': '返回', 'Next': '下一页', 'Previous': '上一页', 'Create': '创建', 'Update': '更新', 'Save': '保存', 'Edit': '编辑', 'Refresh': '刷新', 'Retry': '重试', 'Download CSV': '下载 CSV', 'No data': '暂无数据', 'Never': '从未',
}
Object.assign(zh, {
  'ADMINISTRATION': '系统管理', 'CONTROL PLANE ONLINE': '控制平面在线', 'LIVE OPERATIONS': '实时运行', 'V2 TENANCY': 'V2 租户体系', 'V2 PROJECT GOVERNANCE': 'V2 项目治理', 'SIGNED EVENT DELIVERY': '签名事件投递',
  'Control plane overview': '控制平面概览', 'Gateway traffic, credential health, and routing performance at a glance.': '集中查看网关流量、凭据健康状态与路由性能。', 'Requests': '请求数', 'Token throughput': 'Token 吞吐量', 'Active alerts': '活动告警', 'Request volume trend': '请求量趋势', 'Model distribution': '模型分布', 'View all': '查看全部',
  'Organization governance': '组织治理', 'Create tenant boundaries and control organization membership and status.': '创建租户边界并管理组织成员与状态。', 'Create organization': '创建组织', 'No organizations': '暂无组织', 'Organization status fails closed': '组织状态采用故障关闭', 'Organization-wide roles and status': '组织级角色与状态', 'Disabling or archiving an organization immediately invalidates project API keys under it.': '禁用或归档组织会立即使其下项目 API 密钥失效。',
  'Create project': '创建项目', 'Create a project to grant models and issue isolated API keys.': '创建项目以授权模型并签发隔离的 API 密钥。', 'Create an organization before adding projects or issuing project-scoped keys.': '添加项目或签发项目密钥前，请先创建组织。', 'No projects in this organization': '此组织中暂无项目', 'Tenant scope': '租户范围', 'Members': '成员', 'Member': '成员', 'Owner': '所有者', 'Viewer': '查看者', 'Role': '角色', 'Role *': '角色 *', 'Add member': '添加成员', 'Remove member': '移除成员', 'Save member': '保存成员',
  'Encrypted provider credentials': '加密的提供商凭据', 'Provider secrets encrypted at rest': '提供商密钥静态加密', 'Hashed downstream API keys': '下游 API 密钥仅存哈希', 'Sync models': '同步模型', 'Sync provider models': '同步提供商模型', 'RelayDock uses the selected authorized credential to query the provider\'s official Models API.': 'RelayDock 使用选中的已授权凭据查询提供商官方模型 API。',
  'Project model routes': '项目模型路由', 'Grant model route': '授予模型路由', 'Grant route': '授予路由', 'Remove route grant': '移除路由授权', 'No routes granted': '尚未授权路由', 'Project API keys cannot invoke models until at least one route is granted.': '至少授权一条路由后，项目 API 密钥才能调用模型。', 'Required credential tags': '必需凭据标签', 'Excluded credential tags': '排除凭据标签', 'Every listed tag must be present.': '列出的每个标签都必须存在。', 'Any matching tag makes a credential ineligible.': '匹配任一标签都会使凭据不可用。',
  'Monthly project budget': '项目月度预算', 'Add budget policy': '添加预算策略', 'No project budget policy': '尚无项目预算策略', 'Monthly token limit': '每月 Token 上限', 'Monthly cost limit (USD)': '每月费用上限（USD）', 'Warning threshold (%)': '告警阈值（%）', 'Warn only': '仅告警', 'Block at limit': '达到上限时阻止', 'Enforcement': '执行策略', 'Remove budget policy': '移除预算策略', 'Budget events': '预算事件', 'No budget events': '暂无预算事件',
  'Manage API keys': '管理 API 密钥', 'Create key': '创建密钥', 'Rotate key': '轮换密钥', 'Finalize key rotation': '完成密钥轮换', 'Grace period (minutes) *': '宽限期（分钟）*', 'ONE-TIME SECRET': '一次性密钥', 'Copy your API key': '复制 API 密钥', 'Store this secret in an approved secret manager before closing.': '关闭前请将此密钥保存到获批的密钥管理器。', 'This value will not be displayed again.': '此值不会再次显示。',
  'Routing': '路由', 'General settings': '常规设置', 'Security controls': '安全控制', 'Scheduler defaults': '调度器默认值', 'Operational thresholds': '运行阈值', 'Workspace and retention': '工作区与保留策略', 'Prompt content logging': '提示内容日志', 'Keep disabled unless an approved policy and retention process is in place.': '除非已有获批的策略与保留流程，否则请保持禁用。',
  'Webhook endpoints and delivery attempts are isolated by project.': 'Webhook 端点和投递尝试按项目隔离。', 'Add endpoint': '添加端点', 'Add webhook endpoint': '添加 Webhook 端点', 'No webhook endpoints': '暂无 Webhook 端点', 'Delivery outbox': '投递发件箱', 'No webhook deliveries': '暂无 Webhook 投递', 'Subscribed events *': '订阅事件 *', 'Signing secret *': '签名密钥 *', 'HTTPS URL *': 'HTTPS 地址 *', 'Disable webhook': '禁用 Webhook', 'Dead letter': '死信', 'Delivered': '已投递', 'Retrying': '重试中', 'Pending': '等待中',
  'Unable to load data': '无法加载数据', 'Clear search': '清除搜索', 'Select a project': '选择项目', 'Select route': '选择路由', 'Description': '描述', 'Created': '创建时间', 'Updated': '更新时间', 'Input': '输入', 'Output': '输出', 'Cached': '缓存', 'Filters': '筛选条件', 'Required': '必需', 'Warning': '警告', 'Page not found': '页面不存在', 'Return to dashboard': '返回仪表盘',
})

Object.assign(zh, {
  'Add provider': '添加提供商',
  'Create API key': '创建 API 密钥',
  'Create group': '创建分组',
  'Create route': '创建路由',
  'No api keys': '暂无 API 密钥',
  'No request logs': '暂无请求日志',
  'No alerts': '暂无告警',
  'No credential groups': '暂无凭据分组',
  'No usage': '暂无用量',
  'No audit logs': '暂无审计日志',
  'No records matched the current filters.': '当前筛选条件下没有记录。',
  'Save changes': '保存更改',
  '24 Hours': '24 小时',
  '24 hours': '24 小时',
})
Object.assign(zh, {
  '30 days': '30 天', '7 days': '7 天', 'Today': '今天', 'API key': 'API 密钥', 'API keys': 'API 密钥', 'Acknowledged': '已确认', 'Action': '操作', 'Active credentials': '活动凭据', 'Actor': '操作者', 'Added': '已添加', 'Alert': '告警', 'Alias': '别名', 'Attempts': '尝试次数', 'Audit log retention': '审计日志保留期', 'Audit logs': '审计日志', 'Average latency': '平均延迟', 'Base URL': '基础地址', 'Budget': '预算', 'Cached input': '缓存输入', 'Capabilities': '能力', 'Capacity': '容量', 'Credential cooldown': '凭据冷却时间', 'Credential groups': '凭据分组', 'Credentials': '凭据', 'Default RPM': '默认 RPM', 'Default TPM': '默认 TPM', 'Display name': '显示名称', 'Enabled': '已启用', 'Endpoint': '端点', 'Environment': '环境', 'Errors': '错误数', 'Est. cost': '估算费用', 'Estimated cost': '估算费用', 'Event': '事件', 'Events': '事件', 'Excluded tags': '排除标签', 'Fallback group ID': '备用分组 ID', 'Fallback pool': '备用池', 'Gateway name': '网关名称', 'Group name': '分组名称', 'High 429 rate': '高 429 比例', 'High error rate': '高错误率', 'ID': 'ID', 'Input / 1M': '输入 / 百万 Token', 'Input tokens': '输入 Token', 'Invite user': '邀请用户', 'Key': '密钥', 'Key name': '密钥名称', 'Keys': '密钥数', 'Last HTTP': '最近 HTTP 状态', 'Last check': '最近检查', 'Last delivery': '最近投递', 'Last login': '最近登录', 'Last result': '最近结果', 'Last used': '最近使用', 'Latency': '延迟', 'Least loaded': '最低负载', 'Live': '正式环境', 'Max scheduler attempts': '最大调度尝试次数', 'Minimum healthy pool size': '最小健康池数量', 'Model': '模型', 'Model alias': '模型别名', 'Model registry is empty': '模型注册表为空', 'Model routes': '模型路由', 'Monthly tokens': '每月 Token', 'Name A–Z': '名称 A–Z', 'Newest first': '最新优先', 'No model routes': '暂无模型路由', 'No providers configured': '尚未配置提供商', 'Oldest first': '最早优先', 'Opened': '触发时间', 'Output / 1M': '输出 / 百万 Token', 'Output tokens': '输出 Token', 'Period': '周期', 'Policy': '策略', 'Primary credential group ID': '主凭据分组 ID', 'Primary pool': '主凭据池', 'Priority weighted': '优先级加权', 'Provider type': '提供商类型', 'RPM': 'RPM', 'Recent requests': '近期请求', 'RelayDock alias': 'RelayDock 别名', 'Request ID': '请求 ID', 'Request log retention': '请求日志保留期', 'Request logs': '请求日志', 'Requested model': '请求模型', 'Requests per minute': '每分钟请求数', 'Requests today': '今日请求', 'Required tags': '必需标签', 'Resolved': '已解决', 'Resource': '资源', 'Result': '结果', 'Revoked': '已撤销', 'Route grants': '路由授权', 'Routing policy': '路由策略', 'Severity': '严重程度', 'Signing secret': '签名密钥', 'Slowest first': '最慢优先', 'Slug': '标识符', 'State': '状态', 'Success': '成功', 'Success rate': '成功率', 'Suspended': '已暂停', 'Temporary password': '临时密码', 'Time': '时间', 'Tokens': 'Token', 'Tokens per minute': '每分钟 Token', 'Total requests': '请求总数', 'Type': '类型', 'Unhealthy': '不健康', 'Upstream model': '上游模型', 'Upstream model ID': '上游模型 ID', 'User': '用户', 'User ID': '用户 ID', 'Weighted round robin': '加权轮询',
  'Add an official provider connection to begin configuring authorized credentials.': '添加官方提供商连接后即可配置已授权凭据。', 'Administrative audit history, in days.': '管理操作审计历史的保留天数。', 'Alert below this eligible credential count.': '可用凭据数量低于此值时告警。', 'All recorded gateway traffic': '所有已记录的网关流量', 'All recorded input tokens': '所有已记录的输入 Token', 'All recorded output tokens': '所有已记录的输出 Token', 'Analyze requests, tokens, errors, latency, and configured cost accounting.': '分析请求、Token、错误、延迟和配置的费用核算。', 'Bounded attempts across eligible credentials.': '在符合条件的凭据中执行有限次数的尝试。', 'Completed without gateway or upstream errors': '无网关或上游错误地完成', 'Configure official upstream API providers and connection health.': '配置官方上游 API 提供商及连接健康状态。', 'Connect a provider, then sync its official model catalog.': '连接提供商后同步其官方模型目录。', 'Control user roles, access status, quotas, and issued keys.': '管理用户角色、访问状态、额度和已签发密钥。', 'Create an alias after models and credential groups are configured.': '配置模型和凭据分组后创建别名。', 'Displayed in operator-facing interfaces.': '显示在管理员界面中。', 'Immutable history of administrative and security-sensitive operations.': '不可变的管理与安全敏感操作历史。', 'Inspect gateway decisions and sanitized request metadata. Prompt content is never shown.': '检查网关决策与脱敏请求元数据，绝不显示提示内容。', 'Manage project-scoped downstream keys, versioned rotation, quotas, and rate limits.': '管理项目范围的下游密钥、版本轮换、额度和速率限制。', 'Manage the provider model registry, capabilities, and configured pricing.': '管理提供商模型注册表、能力和配置价格。', 'Map stable RelayDock aliases to provider models and credential pools.': '将稳定的 RelayDock 别名映射到提供商模型和凭据池。', 'Organize credentials into deterministic scheduling pools.': '将凭据组织到确定性调度池中。', 'Percent of requests rate limited.': '被限流请求的百分比。', 'Percent over the evaluation window.': '评估窗口内的百分比。', 'Provider-reported cached tokens': '提供商报告的缓存 Token', 'RelayDock configured pricing': 'RelayDock 配置价格', 'Requests allowed per minute.': '每分钟允许的请求数。', 'Review credential, pool, error-rate, and quota threshold conditions.': '检查凭据、池、错误率和额度阈值状态。', 'Sanitized request metadata, in days.': '脱敏请求元数据的保留天数。', 'Seconds excluded after a retryable upstream response.': '收到可重试上游响应后的排除秒数。', 'Share through an approved secure channel.': '请通过获批的安全渠道分享。', 'The owner must have active access to this project.': '所有者必须拥有此项目的有效访问权限。', 'Tokens allowed per minute.': '每分钟允许的 Token 数。', 'Use the official provider API endpoint.': '使用提供商官方 API 端点。', 'Used only when safe failover criteria are met.': '仅在满足安全故障转移条件时使用。',
})
Object.assign(zh, {
  'Access is managed by your RelayDock administrator.': '访问权限由 RelayDock 管理员管理。', 'Add': '添加', 'Add an HTTPS receiver to subscribe to project events.': '添加 HTTPS 接收端以订阅项目事件。', 'Add an active organization member to this project.': '将组织中的活跃成员添加到此项目。', 'Add an authorized OpenAI API credential before routing traffic.': '路由流量前请添加已授权的 OpenAI API 凭据。', 'Add every selected credential to the target scheduling group.': '将所有选中的凭据添加到目标调度分组。', 'Add organization member': '添加组织成员', 'Add policy': '添加策略', 'Add project member': '添加项目成员',
  'Alert thresholds': '告警阈值', 'All monitored conditions are within configured thresholds.': '所有监控条件均处于配置阈值内。', 'An ungranted alias returns model_not_found before any upstream request is made.': '未授权的别名会在发起上游请求前返回 model_not_found。', 'Archive': '归档', 'At least one token or cost limit is required.': '至少需要设置 Token 或费用上限之一。', 'Audited administrative actions': '已审计的管理操作', 'Authorized credential ID': '已授权凭据 ID', 'Budget event receiver': '预算事件接收端',
  'CSV rows contain bounded project metadata and accounting fields, never prompts, responses, or secrets.': 'CSV 仅包含有限的项目元数据和计费字段，不含提示、响应或密钥。', 'Comma-separated labels used by route required/excluded tag constraints.': '用逗号分隔的标签，供路由的必需/排除标签约束使用。', 'Conditions appear in the Admin dashboard and Alerts page.': '条件会显示在管理仪表盘和告警页面。', 'Confirm all workloads use the newest secret. Older secrets cannot be restored.': '请确认所有工作负载均使用最新密钥，旧密钥无法恢复。', 'Control plane defaults, security posture, scheduler behavior, and alert thresholds.': '控制平面默认值、安全策略、调度器行为与告警阈值。',
  'Copy': '复制', 'Create endpoint': '创建端点', 'Credential APIs return only whether a secret exists and its final four characters.': '凭据 API 仅返回密钥是否存在及其末四位。', 'Credential ID *': '凭据 ID *', 'Current UTC day': '当前 UTC 日期', 'Deduplicated warning and exceeded events for this project': '此项目去重后的告警和超限事件', 'Deliver project-scoped budget and key lifecycle events through a durable outbox.': '通过持久发件箱投递项目范围的预算和密钥生命周期事件。', 'Developer': '开发者',
  'Embedding Pool': '嵌入池', 'Endpoints': '端点', 'Enforced': '已强制执行', 'Estimated cost uses RelayDock configured pricing.': '估算费用采用 RelayDock 配置的价格。', 'Export': '导出', 'Export project usage': '导出项目用量', 'Finalize grace versions': '结束宽限版本', 'For security, RelayDock stores only a hash. You will not be able to retrieve this value later.': '为确保安全，RelayDock 仅保存哈希，之后无法取回此值。', 'From (UTC) *': '起始时间（UTC）*', 'General': '常规',
  'Grant one stable alias and optionally constrain its eligible credential tags.': '授予一个稳定别名，并可选择限制其可用凭据标签。', 'HMAC-SHA256 signed, at-least-once delivery': 'HMAC-SHA256 签名，至少投递一次', 'Immediately revoke every older grace version.': '立即撤销所有旧的宽限版本。', 'Input, cached input, and output tokens': '输入、缓存输入和输出 Token', 'Isolate models, membership, budgets, usage, logs, API keys, and webhook events.': '隔离模型、成员、预算、用量、日志、API 密钥和 Webhook 事件。', 'Loading': '加载中', 'Logging and secret policy': '日志与密钥策略', 'Model route *': '模型路由 *', 'Monitor authorized provider credentials, routing health, usage, and access policy without exposing upstream secrets.': '在不暴露上游密钥的前提下监控已授权凭据、路由健康、用量和访问策略。',
  'Next page': '下一页', 'No active alerts': '暂无活动告警', 'No members': '暂无成员', 'No model usage': '暂无模型用量', 'No project members': '暂无项目成员', 'No request activity': '暂无请求活动', 'No token usage': '暂无 Token 用量', 'Only granted aliases are visible to project keys. Optional tags constrain credential eligibility.': '项目密钥只能看到已授权别名；可选标签用于限制凭据资格。', 'OpenAI provider ID': 'OpenAI 提供商 ID', 'Operate every model route from one control plane.': '通过一个控制平面管理所有模型路由。',
  'Organization': '组织', 'Paste 1 to 25 already-issued official provider API credentials. Account passwords, browser cookies, consumer sessions, and registration data are not accepted.': '粘贴 1 至 25 个已签发的官方提供商 API 凭据。不接受账号密码、浏览器 Cookie、消费者会话或注册数据。', 'Pending, delivered, retrying, and dead-letter events.': '等待中、已投递、重试中和死信事件。', 'Policy name *': '策略名称 *', 'Previous page': '上一页', 'Production API': '生产 API', 'Production Pool': '生产池', 'Production endpoints must use HTTPS and resolve to a public network address.': '生产端点必须使用 HTTPS，并解析到公网地址。', 'Project CSV': '项目 CSV', 'Project members': '项目成员',
  'Prompt content may contain sensitive user data. Review your privacy and retention policy before saving.': '提示内容可能包含敏感用户数据，保存前请审查隐私与保留策略。', 'Provider ID': '提供商 ID', 'Purpose and owning team': '用途与负责团队', 'Queued tests and subscribed project events will appear here.': '排队的测试与已订阅项目事件将显示在这里。', 'Reasoning Pool': '推理池', 'RelayDock Control Plane · Authorized provider API credentials only': 'RelayDock 控制平面 · 仅限已授权的提供商 API 凭据', 'RelayDock never exposes upstream credential plaintext.': 'RelayDock 绝不会暴露上游凭据明文。', 'RelayDock user ID': 'RelayDock 用户 ID',
  'RelayDock validates this administrator-supplied key against the official API before activating it.': 'RelayDock 会先通过官方 API 验证管理员提供的密钥，再将其激活。', "Removing or disabling membership invalidates that user's project keys immediately.": '移除或禁用成员资格会立即使该用户的项目密钥失效。', 'Request volume': '请求量', 'Requests resolved by the gateway over the last 24 hours': '网关过去 24 小时处理的请求', 'Required tags must all match; any excluded tag removes this credential from the candidate set.': '所有必需标签都必须匹配；任一排除标签都会将凭据移出候选集。', 'Rotate': '轮换', 'Rotate project API key': '轮换项目 API 密钥',
  'Routes relying on these credentials may lose capacity. This action is recorded in the audit log.': '依赖这些凭据的路由可能损失容量，此操作会记录在审计日志中。', 'Save policy': '保存策略', 'Save the rotated API key': '保存轮换后的 API 密钥', 'Search RelayDock administration pages.': '搜索 RelayDock 管理页面。', 'Secrets are sent only to RelayDock, encrypted before storage, and never returned in list or export responses.': '密钥只发送给 RelayDock，加密保存，且不会在列表或导出响应中返回。', 'Security': '安全',
  'Select': '选择', 'Select at least one event type.': '请至少选择一种事件类型。', 'Select organization': '选择组织', 'Select project': '选择项目', 'Select…': '请选择…', 'Set a token limit, a cost limit, or both.': '设置 Token 上限、费用上限或两者。', 'Share of resolved requests': '已处理请求占比', 'Sign in to RelayDock': '登录 RelayDock', 'Signing secrets are encrypted and never returned.': '签名密钥会被加密且绝不返回。', 'Slug *': '标识符 *', 'Status *': '状态 *',
  'The new secret is shown once; the old secret remains valid during the grace window.': '新密钥仅显示一次；宽限期内旧密钥仍然有效。', 'The original provider secret is never returned by the API. Enter a new value to replace it.': 'API 永不返回原始提供商密钥；请输入新值进行替换。', 'The plaintext secret is never displayed after creation.': '创建后不再显示密钥明文。', 'The requested RelayDock admin page does not exist.': '请求的 RelayDock 管理页面不存在。', 'The signed-in administrator becomes the initial owner.': '当前登录的管理员将成为初始所有者。', 'The user must already exist in RelayDock.': '该用户必须已存在于 RelayDock。', 'The user must have an active membership in the parent organization.': '该用户必须是上级组织的活跃成员。',
  'This permanently removes the selected encrypted credentials from RelayDock.': '这将从 RelayDock 永久删除选中的加密凭据。', 'This secret is shown once. Store it in a secure secret manager.': '此密钥仅显示一次，请将其保存到安全的密钥管理器。', 'To (UTC) *': '结束时间（UTC）*', 'Traffic is admitted using key and user limits only.': '流量仅根据密钥和用户限额准入。', 'Traffic will appear here after the gateway receives requests.': '网关收到请求后，流量将显示在这里。', 'Use a random secret from your secret manager': '使用密钥管理器中的随机密钥', 'Use your administrator account to continue.': '使用管理员账号继续。', 'Used when a route or key does not provide a more specific limit.': '当路由或密钥未提供更具体限额时使用。', 'User ID *': '用户 ID *',
  'Verify the exact request body with the timestamp and signature headers, then deduplicate by event ID. Redirects are never followed.': '使用时间戳和签名头验证原始请求体，然后按事件 ID 去重；绝不跟随重定向。', 'WARN emits a deduplicated event; BLOCK rejects before upstream dispatch.': 'WARN 发出已去重事件；BLOCK 在上游分发前拒绝请求。', 'Workspace identity and data retention.': '工作区标识与数据保留。',
})

const I18nContext = createContext<{ language: Language; setLanguage: (language: Language) => void; t: (value: string) => string }>({ language: 'en', setLanguage: () => undefined, t: (value) => value })
const textOriginals = new WeakMap<Node, string>()
const attributeOriginals = new WeakMap<Element, Map<string, string>>()
const translatedAttributes = ['aria-label', 'placeholder', 'title']

function translateValue(value: string) {
  if (zh[value]) return zh[value]
  const statuses: Record<string, string> = { active: '启用', disabled: '禁用', archived: '已归档', healthy: '健康', unhealthy: '不健康', unknown: '未知', available: '可用', success: '成功', warning: '警告', error: '错误', open: '未处理', acknowledged: '已确认', resolved: '已解决', pending: '等待中', retrying: '重试中', delivered: '已投递', dead: '死信', revoked: '已撤销', enabled: '已启用', viewer: '查看者', member: '成员', owner: '所有者', admin: '管理员', super_admin: '超级管理员', user: '用户', yes: '是', no: '否' }
  if (statuses[value.toLowerCase()]) return statuses[value.toLowerCase()]
  let match = value.match(/^(\d+) selected$/)
  if (match) return `已选择 ${match[1]} 项`
  match = value.match(/^Delete (\d+) credentials$/)
  if (match) return `删除 ${match[1]} 个凭据`
  match = value.match(/^Select (.+)$/)
  if (match) return `选择 ${match[1]}`
  match = value.match(/^Cooldown until (.+)$/)
  if (match) return `冷却截止 ${match[1]}`
  match = value.match(/^(\d+) authorized accounts$/)
  if (match) return `${match[1]} 个已授权账号`
  match = value.match(/^(\d+) ready$/)
  if (match) return `${match[1]} 个可用`
  match = value.match(/^Snapshot (.+)$/)
  if (match) return `快照 ${match[1]}`
  match = value.match(/^Live verification passed · (.+)$/)
  if (match) return `实时验证通过 · ${match[1]}`
  match = value.match(/^(\d+)–(\d+) of (\d+)$/)
  if (match) return `${match[1]}–${match[2]} / 共 ${match[3]} 条`
  match = value.match(/^(\d+) results?$/)
  if (match) return `共 ${match[1]} 条`
  match = value.match(/^Page (\d+) of (\d+)$/)
  if (match) return `第 ${match[1]} / ${match[2]} 页`
  match = value.match(/^(\d+) healthy · (\d+) limited$/)
  if (match) return `${match[1]} 个健康 · ${match[2]} 个已限流`
  return value
}

function localizeText(node: Node, language: Language) {
  if (node.parentElement?.closest('script, style, code, pre, [data-no-translate]')) return
  if (language === 'en') { const original = textOriginals.get(node); if (original !== undefined && node.nodeValue !== original) node.nodeValue = original; return }
  const current = node.nodeValue || ''; const trimmed = current.trim(); if (!trimmed) return
  const translated = translateValue(trimmed); if (translated === trimmed) return
  textOriginals.set(node, current); node.nodeValue = current.replace(trimmed, translated)
}

function localizeElement(element: Element, language: Language) {
  let originals = attributeOriginals.get(element)
  for (const attribute of translatedAttributes) {
    if (language === 'en') { const original = originals?.get(attribute); if (original !== undefined) element.setAttribute(attribute, original); continue }
    const current = element.getAttribute(attribute); if (!current) continue
    const translated = translateValue(current); if (translated === current) continue
    if (!originals) { originals = new Map(); attributeOriginals.set(element, originals) }
    originals.set(attribute, current); element.setAttribute(attribute, translated)
  }
}

function localizeTree(root: Node, language: Language) {
  if (root.nodeType === Node.TEXT_NODE) localizeText(root, language)
  if (root.nodeType === Node.ELEMENT_NODE) localizeElement(root as Element, language)
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT)
  let node: Node | null
  while ((node = walker.nextNode())) node.nodeType === Node.TEXT_NODE ? localizeText(node, language) : localizeElement(node as Element, language)
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, setLanguage] = useState<Language>(() => { const saved = localStorage.getItem(storageKey); return saved === 'en' || saved === 'zh' ? saved : 'zh' })
  useEffect(() => {
    localStorage.setItem(storageKey, language); document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en'; localizeTree(document.body, language)
    const observer = new MutationObserver((mutations) => { for (const mutation of mutations) { if (mutation.type === 'characterData') localizeText(mutation.target, language); if (mutation.type === 'attributes') localizeElement(mutation.target as Element, language); mutation.addedNodes.forEach((node) => localizeTree(node, language)) } })
    observer.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: translatedAttributes })
    return () => observer.disconnect()
  }, [language])
  const value = useMemo(() => ({ language, setLanguage, t: (text: string) => language === 'zh' ? translateValue(text) : text }), [language])
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() { return useContext(I18nContext) }
export function LanguageToggle({ compact = false }: { compact?: boolean }) {
  const { language, setLanguage } = useI18n()
  return <div className={`language-toggle ${compact ? 'compact' : ''}`} role="group" aria-label="Language"><button type="button" className={language === 'en' ? 'active' : ''} onClick={() => setLanguage('en')} title="Switch to English">EN</button><button type="button" className={language === 'zh' ? 'active' : ''} onClick={() => setLanguage('zh')} title="Switch to Chinese">中文</button></div>
}
export function currentLocale() { return localStorage.getItem(storageKey) === 'en' ? 'en-US' : 'zh-CN' }
