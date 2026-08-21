# 数据处理说明（架构事实草案）

> **待律师审核 / PENDING COUNSEL REVIEW — 非最终 DPA、隐私声明或跨境评估。**

## 数据流

```text
客户应用
  -> ModelDock API Key 验证、地区/合同/预算/计费准入
  -> 获准的 Provider 与模型
  -> Provider 返回结果
  -> ModelDock 保存请求/用量/价格/资金/审计元数据
```

默认日志不保存 prompt/response 正文。推理内容在请求期间会传给实际选中的 Provider；
SSE 开始后不跨 Provider 重放。公开 Provider 目录披露允许运营地区、数据处理地区、
Provider 数据保留政策和条款版本；缺失值必须显示未知或不可用，不能猜测。

ModelDock 将 PostgreSQL 作为账本/审计事实源，将 Redis 用于可重建的限流、容量和锁
状态。认证 Token、邮箱验证、TOTP、Provider/BYOK 密钥、付款 Webhook 和备份采用各自
的安全边界。财务和审计证据可能因法律义务或 legal hold 不能应用户请求立即删除。

## 待审核附件

律师与隐私负责人必须确定控制者/处理者角色、处理指令、保密、安全措施、子处理者
通知、协助权利请求、事件通知、删除/返还、审计权、跨境传输和责任。运营方必须维护
与运行时 Provider/邮件/支付/托管/观测配置一致的子处理者清单，不能使用静态营销
清单替代实际配置。

