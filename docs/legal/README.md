# Legal and policy review pack

> **待律师审核 / PENDING COUNSEL REVIEW — 非最终法律文件。** 本目录由产品与
> 工程团队记录系统事实和待填写条款，不能直接作为对外最终法律文本。上线前必须由
> 适用法域的执业律师、税务和隐私负责人审核，并由有权代表公司的人批准版本号与
> 生效日期。

每一个公开法律/政策页面必须持续显示上述审核状态，直到以下占位均被批准文本替换：

- 运营主体法定全称、注册地址、统一社会信用代码/注册号；
- ICP 备案、ICP 许可证、公安备案等实际适用编号（不得伪造）；
- 适用法、争议解决地、消费者强制性权利；
- 隐私控制者/处理者身份、法定处理依据、未成年人规则、跨境机制；
- 税费、发票、退款期限、支付渠道和渠道费用；
- 各 Provider 合同、转售许可、地区、数据处理和保留条款；
- 投诉、隐私、安全、法律送达和企业销售的真实联系渠道；
- 中文与其他语言版本的优先顺序。

页面源文件：

- [service-terms-draft.md](service-terms-draft.md)
- [privacy-policy-draft.md](privacy-policy-draft.md)
- [acceptable-use-policy-draft.md](acceptable-use-policy-draft.md)
- [refund-policy-draft.md](refund-policy-draft.md)
- [data-processing-notice-draft.md](data-processing-notice-draft.md)
- [provider-model-disclosure-draft.md](provider-model-disclosure-draft.md)
- [complaints-company-info-draft.md](complaints-company-info-draft.md)

网站展示的商业事实必须来自运行时公开接口，而不是复制进法律文档的固定值：
`GET /api/public/pricing`、`GET /api/public/catalog/models`、
`GET /api/public/catalog/providers` 和 `GET /api/public/status`。

