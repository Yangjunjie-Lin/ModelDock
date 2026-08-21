import { Scale, ShieldAlert, TriangleAlert } from 'lucide-react'
import { Link, useParams } from 'react-router-dom'

type LegalDocument = {
  title: string
  summary: string
  sections: Array<{ title: string; paragraphs?: string[]; bullets?: string[] }>
}

const documents: Record<string, LegalDocument> = {
  terms: {
    title: '服务条款',
    summary: '本草案描述账户、API 使用、费用、暂停与责任边界，不能替代由适用法域律师审核的正式条款。',
    sections: [
      { title: '服务范围', paragraphs: ['ModelDock 提供 OpenAI-compatible 网关、控制台、模型路由、用量与账务能力。具体模型、Provider、地区、容量和功能以实时公开接口、控制台授权及双方书面合同为准。'] },
      { title: '账户与密钥', bullets: ['用户应提供真实、合法且有权使用的账户信息。', 'API Key 仅供授权项目使用，不得转售、共享或用于规避限制。', '完整密钥只显示一次；用户应使用密钥管理器妥善保存。'] },
      { title: '费用与支付', paragraphs: ['订阅费、Token 按量费、支付渠道或平台服务费、税费和赠送额度分别披露。最终结算以请求价格快照、支付证据与账本记录为准。'] },
      { title: '暂停与终止', paragraphs: ['在安全风险、欠费、合同或地区限制、Provider 禁用、滥用调查或法律要求下，服务可能限制或暂停相关账户、项目、模型或 Provider。具体通知与救济流程必须由律师和运营方确认。'] },
    ],
  },
  privacy: {
    title: '隐私政策',
    summary: '本草案说明可能处理的数据类别和控制措施；正式版本需结合实际运营主体、基础设施、子处理者和适用法律完成律师审核。',
    sections: [
      { title: '可能处理的数据', bullets: ['账户身份、组织和项目成员信息。', '脱敏请求元数据、Token、时延、状态、路由与请求 ID。', '支付订单、钱包、账本、订阅、退款与发票申请证据。', '安全、审计、支持工单与数据生命周期事件。'] },
      { title: '处理目的', paragraphs: ['用于提供服务、身份与访问控制、路由、计费、反滥用、排障、支持、审计和履行适用的合同或法律义务。'] },
      { title: '保留与删除', paragraphs: ['保留期限由组织设置、部署配置、Provider 政策、合同和适用法律共同决定。通用页面不承诺固定期限；用户可在控制台查看或发起支持的导出、关闭与删除流程。'] },
      { title: '跨境与子处理者', paragraphs: ['数据处理地区和 Provider 披露需在购买与调用前核对。跨境路径、子处理者清单与合法性基础必须由运营方和律师在正式政策中补全。'] },
    ],
  },
  'acceptable-use': {
    title: '可接受使用政策',
    summary: '本草案列出禁止和受限行为；正式分类、申诉与执法标准仍需由律师、安全和运营团队审核。',
    sections: [
      { title: '明确禁止', bullets: ['账号批量注册、验证码绕过或身份欺诈。', '代理规避、地区限制绕过或安全机制绕过。', 'API Key、Provider 账户或消费者账户买卖、出租或未授权共享。', '侵犯他人权利、违法内容、恶意软件、凭据窃取或支付欺诈。', '规避配额、限流、内容政策或 Provider 条款。'] },
      { title: '用户责任', paragraphs: ['用户必须确保其输入、输出使用、数据来源、最终产品和 Provider 使用方式符合适用法律、合同、模型政策与第三方权利。ModelDock 的技术控制不构成对用户业务的合规结论。'] },
      { title: '处置与申诉', paragraphs: ['服务可能冻结密钥、限制模型、暂停账户或进入人工审核。正式政策需明确通知、证据保留、申诉期限和紧急处置程序。'] },
    ],
  },
  refunds: {
    title: '退款规则',
    summary: '本草案解释系统当前的资格评估边界，不构成无条件退款承诺；正式规则需结合支付渠道、消费者法和合同由律师审核。',
    sections: [
      { title: '分项评估', bullets: ['未使用的充值现金余额。', '已消耗的 Token、订阅服务或其他已提供服务。', '不可现金退款的赠送额度。', '已产生且不可逆的 Provider 成本。', '支付渠道限制、税费和退款手续费。'] },
      { title: '申请流程', paragraphs: ['登录控制台提交退款申请并选择来源订单、金额和理由。服务端依据不可变支付、钱包、用量、订阅、Provider 成本和账本证据审核；提交申请不等于批准或到账。'] },
      { title: '特殊情形', paragraphs: ['拒付、支付渠道失败、重复支付、疑似欺诈、账户关闭与法定撤回权的处理方式需要按地区和支付合同在正式版本中补全。'] },
    ],
  },
  'data-processing': {
    title: '数据处理说明',
    summary: '本草案提供技术处理视图，不是数据处理协议（DPA）或跨境传输法律意见。',
    sections: [
      { title: '请求路径', bullets: ['客户端使用 rdk_* 密钥向 ModelDock /v1 发起请求。', 'ModelDock 执行项目、模型、Provider、地区、资金、安全和健康准入。', '合格请求发送给已配置的官方 Provider API。', '控制台请求日志展示脱敏元数据，不展示提示或响应正文。'] },
      { title: 'Provider 数据处理', paragraphs: ['Provider 的数据处理地区、保留政策与条款版本在公开 Provider 目录中披露。实际处理仍受具体模型、组织路由、Provider 条款和合同约束。'] },
      { title: '组织控制', bullets: ['项目与 API Key 范围。', 'Provider 与模型允许/禁止规则。', '内容分类、保留和跨境路由设置。', 'BYOK 官方凭据所有权确认。', '数据导出和生命周期请求。'] },
      { title: '仍需合同确认', paragraphs: ['控制者/处理者角色、子处理者通知、国际传输机制、数据主体请求时限、安全附件和事件通知条款必须由相关方及律师在正式 DPA 中确认。'] },
    ],
  },
  providers: {
    title: 'Provider 与模型披露',
    summary: '本页面解释目录字段和边界；具体 Provider、模型、价格与地区状态必须读取实时公开接口。',
    sections: [
      { title: '公开字段', bullets: ['Provider 合同与转售状态、启用状态和 kill switch。', '允许/禁止地区、数据处理地区与数据保留政策。', '模型能力、上下文窗口、地区可用性与不可用原因。', '输入、缓存输入、输出和固定请求费的有效期价格。'] },
      { title: '可用性边界', paragraphs: ['Provider 或模型被列出不代表其在当前地区可用。购买前查看 available 字段，调用时仍会重新执行实时准入。目录更新、Provider 事件、合同变化或 kill switch 可能改变可用性。'] },
      { title: 'fallback 披露', paragraphs: ['fallback 仅在已配置且候选满足相同商业、安全、地区、所有权和健康约束时发生；不会被用来绕过 Provider 条款或地区限制。'] },
      { title: '实时入口', paragraphs: ['请前往模型目录、Provider 状态、定价页和状态页核对当前部署事实。'] },
    ],
  },
  complaints: {
    title: '投诉与举报入口',
    summary: '本草案说明安全提交方式与分类；法定投诉渠道、处理期限和监管机构信息仍需由运营方及律师补全。',
    sections: [
      { title: '可报告事项', bullets: ['内容或模型滥用。', 'API Key、账户或身份风险。', '订单、扣费、退款或发票申请争议。', '隐私、数据导出、删除或跨境问题。', 'Provider、地区或服务声明不一致。'] },
      { title: '提交方式', paragraphs: ['已登录用户应通过控制台账户页或支持工单提交，并可附 RelayDock 请求 ID、订单号或账本编号。无法登录时使用公开联系页中由部署方配置的支持邮箱。'] },
      { title: '请勿提交', bullets: ['完整 API Key 或 Provider 密钥。', '密码、验证码或支付凭据。', '不必要的个人敏感信息。', '完整提示或响应正文；优先提供脱敏请求 ID。'] },
    ],
  },
  company: {
    title: '公司与备案信息',
    summary: '本部署尚未在公开配置中提供完整运营主体与备案数据。生产商业运营前，必须由运营方填写并经律师/合规人员核验。',
    sections: [
      { title: '运营主体占位', bullets: ['公司法定名称：待运营方填写并核验。', '统一社会信用代码或注册编号：待运营方填写并核验。', '注册地址与联系地址：待运营方填写并核验。', '法定代表人/负责人：待运营方按适用要求披露。'] },
      { title: '备案与许可占位', bullets: ['ICP 备案/许可证：待部署域名和运营模式确认后填写。', '公安联网备案：如适用，待完成后填写。', '增值电信、支付、税务或其他许可：由专业顾问判断适用性并填写。'] },
      { title: '联系信息占位', bullets: ['客户支持邮箱：以联系支持页的部署配置为准。', '隐私/数据保护联系人：待运营方填写。', '法律通知地址：待运营方填写。'] },
    ],
  },
}

export function LegalPage() {
  const { document = 'terms' } = useParams()
  const content = documents[document]
  if (!content) return <div className="legal-page"><div className="public-container"><h1>法律页面不存在</h1><Link to="/legal/terms">返回服务条款</Link></div></div>
  return <div className="legal-page"><div className="public-container legal-layout"><aside><div className="legal-review-stamp"><ShieldAlert size={20} /><span><strong>待律师审核</strong><small>DRAFT — NOT LEGAL ADVICE</small></span></div><nav aria-label="法律与政策页面">{Object.entries(documents).map(([key, value]) => <Link className={key === document ? 'active' : undefined} key={key} to={`/legal/${key}`}>{value.title}</Link>)}</nav></aside><article><div className="legal-alert" role="alert"><TriangleAlert size={20} /><div><strong>重要：本页面是待律师审核的工作草案</strong><p>不得将模型生成或仓库中的本草案直接视为最终法律文件、合规结论或正式承诺。上线前必须由适用法域律师、运营主体和业务负责人审核、补全并批准。</p></div></div><header><span><Scale size={16} />LEGAL DRAFT</span><h1>{content.title}</h1><p>{content.summary}</p><small>草案状态：待律师审核 · 页面版本：2026-08-16</small></header>{content.sections.map((section) => <section key={section.title}><h2>{section.title}</h2>{section.paragraphs?.map((paragraph) => <p key={paragraph}>{paragraph}</p>)}{section.bullets && <ul>{section.bullets.map((bullet) => <li key={bullet}>{bullet}</li>)}</ul>}</section>)}<footer><strong>待完成的正式发布流程</strong><p>确认运营主体、适用法律、服务地区、合同结构、Provider 与子处理者、支付与税务、消费者权利、数据路径和投诉渠道；律师审核后由授权负责人批准并记录版本与生效日期。</p></footer></article></div></div>
}
