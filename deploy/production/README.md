# RelayDock 单机生产部署（Ubuntu 24.04）

> Provider commercial governance and pricing hardening are mandatory after migrations 14 and 15. Review legal
> entity, resale permission, dates, regions, processing residency, organization
> policy, limits/budget, currency, terms and current margin. Technical health
> never substitutes for `COMMERCIAL_APPROVED`. Rehearse the audited emergency
> kill switch; see `docs/provider-governance.md` in the source release.

该部署包面向日本东京或新加坡的低成本 VPS，默认启动六个持续运行的生产服务：

1. RelayDock 网关
2. PostgreSQL 17
3. Redis 7.4
4. Nginx
5. Certbot
6. ModelDock 公开网站与用户 Console

推荐最低规格为 2 vCPU、2 GB RAM、40 GB SSD、Ubuntu 24.04，不需要 GPU。100–500 个用户是容量目标而不是无条件保证；实际并发取决于请求体、流式连接时长、用户限额和上游延迟。上线前应按真实流量做压测。

`api.example.com` 和 `console.example.com` 是保留示例域名，无法为普通用户
签发生产证书。命令中必须换成两个由自己控制、DNS 均指向该边缘入口的真实域名。

## 架构与安全边界

```text
Internet
  -> VPS/云防火墙 (22, 80, 443)
  -> Nginx TLS + per-IP limit_req/limit_conn
     -> API 域名: RelayDock :8080 (/v1, health, payment webhook)
     -> 公开站域名: Console 静态站 + :8081 (/api/public, /api/console)
                    + RelayDock :8080 (/v1)
  -> OpenAI / DeepSeek / OpenRouter 官方 OpenAI-compatible API

RelayDock
  -> PostgreSQL（权威数据、加密后的上游密钥、审计与脱敏请求元数据）
  -> Redis（API Key RPM/TPM、并发计数和调度状态）
```

PostgreSQL、Redis 和 RelayDock 副本不发布宿主机端口。公网 Nginx 通过内部网络
负载均衡到无状态副本；诊断探针通过 `docker compose exec` 执行。既有 API 域名
继续只转发 `/v1/*`、`/healthz` 和经过签名校验的
`/api/payments/webhooks/*`。独立公开站域名只转发 Console 静态站、
`/api/public/*`、用户域 `/api/console/*` 及 `/v1/*`。它没有
`/api/admin/*` 代理；管理面、数据库、Redis 和 Docker socket 均不公开。
Console 登录使用 host-only HttpOnly Cookie 和 `X-CSRF-Token` double-submit，
`ALLOWED_ORIGINS` 必须精确包含公开站 HTTPS Origin。

充值订单能力默认关闭。`sandbox` 仅用于隔离测试，`manual_transfer` 仅用于管理员核验凭证后的人工转账；二者都不是已签约生产支付通道。API 域名只把 `/api/payments/webhooks/*` 转发到控制平面，并保持请求体字节不变；其他控制接口仍返回 404。公开站只暴露用户域，不能调用管理员人工审批。支付签名密钥必须由 secret manager 或进程环境注入，不得写入 Compose 文件、数据库或日志。完整开关、时钟、防重放、恢复、迁移和回滚说明见 [`docs/payments.md`](../../docs/payments.md)。

订阅生命周期由每个应用副本上的幂等 Worker 处理，默认轮询间隔为
`RELAYDOCK_SUBSCRIPTION_POLL_INTERVAL=1m`。订阅费使用独立系统科目，不进入充值钱包；Token 仍始终按量结算。部署、对账、迁移 0011 和回滚边界见
[`docs/subscriptions.md`](../../docs/subscriptions.md)。

RelayDock 下游 API Key 按用户、组织和项目隔离；上游密钥使用 AES-256-GCM 加密且不会返回给客户端。请求日志默认只保存模型、Token、状态码、延迟和请求 ID，不保存提示词、响应正文、Authorization 或 Cookie。Redis 不可用时限流与调度故障关闭。

## 目录结构

执行准备脚本后：

```text
/opt/relaydock/
├── docker-compose.yml
├── .env                         # 0600，禁止提交
├── src/                         # Git 仓库，保持干净以便升级
├── nginx/
│   ├── nginx.conf
│   └── conf.d/relaydock.conf
├── data/
│   ├── postgres/
│   ├── redis/
│   ├── relaydock/logs/
│   ├── nginx/logs/
│   ├── certbot/{conf,www,logs}/
│   └── backups/
```

## 1. 服务器初始化命令

优先选择东京或新加坡机房，并先用目标用户地区测试 RTT 和丢包。可选择提供固定 IPv4、快照和云防火墙的常规 VPS；不建议把难以补货或无 SLA 的免费实例作为唯一生产节点。

首次登录后：

```bash
sudo -i
timedatectl set-timezone Asia/Tokyo   # 新加坡可改 Asia/Singapore
apt-get update
apt-get -y upgrade
reboot
```

如果仓库已经通过镜像或 Deploy Key 放到 `/opt/relaydock`，可运行初始化脚本；否则直接按下一节手动安装 Docker，克隆仓库后再执行该脚本。它同时配置 UFW、Fail2ban 和无人值守安全更新：

```bash
sudo bash /opt/relaydock/src/deploy/production/scripts/bootstrap-ubuntu.sh
```

云厂商安全组也只开放 TCP `22`、`80`、`443`。建议将 SSH 22 仅开放给固定办公 IP，禁用密码登录并使用 SSH Key。

## 2. Docker 安装命令

初始化脚本已经执行以下官方 Docker 安装流程；手动执行时使用：

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

cat <<EOF | sudo tee /etc/apt/sources.list.d/docker.sources
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: noble
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF

sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker containerd
docker --version
docker compose version
```

## 3. 项目部署命令

私有 GitHub 仓库建议使用只读 Deploy Key：

```bash
sudo install -d -m 0750 /opt/relaydock
sudo git clone git@github.com:Yangjunjie-Lin/RelayDock.git /opt/relaydock/src
sudo chown -R "$USER:$USER" /opt/relaydock
cd /opt/relaydock/src
```

生成随机数据库、Redis、AES、HMAC、JWT 和管理员密码：

```bash
bash deploy/production/scripts/generate-env.sh \
  /opt/relaydock \
  api.your-domain.com \
  admin@your-domain.com \
  ops@your-domain.com \
  console.your-domain.com
```

最后一个参数是独立公开站/Console 域名。为兼容已有 API-only 部署它是可选的；
省略时不会创建公开站 TLS 入口。要启用本步骤的完整商业体验，必须提供它，并确保
它与 `API_DOMAIN` 不同。`RELAYDOCK_PUBLIC_CONSOLE_URL`、
`RELAYDOCK_PUBLIC_SITE_DOMAIN` 和 `ALLOWED_ORIGINS` 必须指向同一个 HTTPS 站点。

该命令只打印一次初始管理员密码。立即存入密码管理器。随后准备持久化目录、Nginx HTTP 引导配置和 systemd 兜底：

```bash
sudo bash /opt/relaydock/src/deploy/production/scripts/prepare-host.sh /opt/relaydock /opt/relaydock/src
sudo bash /opt/relaydock/src/deploy/production/scripts/deploy.sh /opt/relaydock
```

独立 `migration` job 使用 PostgreSQL advisory lock 顺序应用嵌入式迁移；应用副本通过 `RELAYDOCK_MIGRATION_MODE=external` 等待 job 成功。不要手工修改已经发布的迁移文件。

查看状态：

```bash
cd /opt/relaydock
sudo docker compose ps
sudo docker compose exec -T relaydock wget -qO- http://127.0.0.1:8080/healthz
sudo docker compose exec -T relaydock wget -qO- http://127.0.0.1:8080/readyz
```

所有主服务都配置 `restart: always`。`relaydock-compose.service` 是 Docker 自动恢复之外的 systemd 兜底：

```bash
sudo systemctl start relaydock-compose
sudo systemctl status relaydock-compose
```

## 4. 环境变量说明

| 变量 | 说明 |
| --- | --- |
| `API_DOMAIN` | 实际 API 域名，必须与 DNS 和证书一致 |
| `RELAYDOCK_PUBLIC_SITE_DOMAIN` | 独立公开网站/Console 域名；为空时保留旧的 API-only 入口 |
| `RELAYDOCK_PUBLIC_CONSOLE_URL` | 验证邮件和跳转使用的完整公开站 HTTPS URL，必须与公开站域名一致 |
| `RELAYDOCK_PUBLIC_SUPPORT_EMAIL` | 公开支持、投诉邮箱；`example.invalid` 只是不可投递占位，上线前必须替换为受监控的角色邮箱 |
| `RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL` | 公开企业服务邮箱；`example.invalid` 只是不可投递占位，上线前必须替换 |
| `LETSENCRYPT_EMAIL` | Let's Encrypt 到期与安全通知邮箱 |
| `POSTGRES_*` / `DATABASE_URL` | PostgreSQL 名称、用户、密码与容器内连接串 |
| `POSTGRES_MAX_CONNS` | RelayDock 数据库池上限，2 GB 单机建议 20 |
| `REDIS_*` / `REDIS_URL` | Redis 密码、连接串和客户端池参数 |
| `RELAYDOCK_MASTER_KEY` | 恰好 32 个解码字节；用于上游密钥 AES-256-GCM 加密，必须独立备份 |
| `RELAYDOCK_API_KEY_HMAC_SECRET` | 下游 RelayDock API Key 的 HMAC 哈希密钥 |
| `RELAYDOCK_JWT_SECRET` | 控制平面登录会话签名密钥 |
| `RELAYDOCK_ADMIN_*` | 第一次启动时创建的管理员；之后修改不会自动重置密码 |
| `ALLOWED_ORIGINS` | 精确 CORS Origin 列表，逗号分隔；禁止使用 `*` |
| `TRUSTED_PROXIES` | 可提供 `X-Forwarded-For` 的代理 CIDR；默认仅 Docker 172.16/12 与 loopback |
| `MAX_REQUEST_BODY_BYTES` | RelayDock 请求体上限；Nginx 同时限制为 10 MB |
| `CREDENTIAL_COOLDOWN` | 上游 429/可重试失败后的默认冷却时间 |
| `RELAYDOCK_PROVIDER_QUALITY_PROBE_REGION` | 本副本真实出口地区；为空时安全禁用定时 Provider 探测 |
| `RELAYDOCK_PROVIDER_QUALITY_POLL_INTERVAL` | 质量探测与评估 worker 轮询间隔 |
| `RELAYDOCK_PROVIDER_QUALITY_LEASE` | 多副本 PostgreSQL 探测租约时长 |
| `RELAYDOCK_PROVIDER_QUALITY_BATCH_SIZE` | 单轮最大探测任务数（上限 100） |
| `LOG_LEVEL` | 生产建议 `info`；不要启用提示词正文日志 |

公开联系邮箱会由 `/api/public/config` 返回，不应填写个人隐私邮箱。发布前必须验证两
个角色邮箱均能收件并有明确响应负责人。

不要把 OpenAI、DeepSeek 或 OpenRouter Key 写进 `.env`。它们应通过 RelayDock 管理面添加，然后以密文进入 PostgreSQL。`.env`、数据库备份和 `RELAYDOCK_MASTER_KEY` 需要分开加密保管。

## 5. DNS 配置方式

在 DNS 提供商创建：

| 类型 | 名称 | 值 | TTL |
| --- | --- | --- | --- |
| `A` | `api` | VPS 公网 IPv4 | 300 |
| `A` | `console` | VPS 公网 IPv4 | 300 |
| `AAAA` | `api` | VPS IPv6（仅确认 IPv6 防火墙和监听正常后） | 300 |
| `AAAA` | `console` | VPS IPv6（仅确认 IPv6 防火墙和监听正常后） | 300 |

验证：

```bash
dig +short A api.your-domain.com
dig +short A console.your-domain.com
dig +short AAAA api.your-domain.com
dig +short AAAA console.your-domain.com
```

结果必须指向该 VPS。使用 Cloudflare 等代理时，首次 HTTP-01 签发建议先设为 DNS only；确认 HTTPS 后再决定是否开启代理。不要在 DNS 尚未生效时反复请求证书，以免触发 Let's Encrypt 限制。

## 6. HTTPS 配置方式

确认 80/443 可从公网访问且 DNS 正确，然后：

```bash
cd /opt/relaydock
sudo bash src/deploy/production/scripts/enable-https.sh /opt/relaydock
```

测试签发流程可先在 `.env` 临时增加 `LETSENCRYPT_STAGING=true`；测试证书不受浏览器信任。正式签发前删除该变量。

Certbot 每 12 小时检查续期，Nginx 每 6 小时 reload 一次以载入新证书。验证：

```bash
curl -I https://api.your-domain.com/healthz
curl -I https://console.your-domain.com/
curl -fsS 'https://console.your-domain.com/api/public/pricing?region=CN&currency=CNY'
openssl s_client -connect api.your-domain.com:443 -servername api.your-domain.com </dev/null 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates
```

## 配置 OpenAI、DeepSeek 和 OpenRouter

迁移会保留内置 OpenAI，并新增以下 OpenAI-compatible 提供商记录：

- OpenAI：`https://api.openai.com/v1`
- DeepSeek：`https://api.deepseek.com/v1`
- OpenRouter：`https://openrouter.ai/api/v1`

为每个提供商添加官方 API Key、创建独立凭据组和模型路由，再把路由授权给项目。建议别名分别使用 `openai-gpt`、`deepseek-chat`、`openrouter-auto`，避免同一别名在不同上游之间发生不可见语义变化。

管理员界面仍作为可选 profile 只绑定到 loopback。用户 Console 是默认启动的公开站，
但其容器端口仍只绑定 loopback；公网流量必须经过 Nginx：

```bash
cd /opt/relaydock
sudo docker compose --profile control-ui up -d admin-web
ssh -L 3000:127.0.0.1:3000 user@your-vps
```

然后只在 SSH 隧道存续期间访问 `http://127.0.0.1:3000`。不要把 3000、
3001 或 8081 加入云防火墙公网规则。公开用户从
`https://console.your-domain.com` 访问，其他 `/api/*`（包括管理路由）在该域名上
由外层 Nginx 返回 404，且不会被
转发到控制平面。

DeepSeek/OpenRouter 通过同一个经过测试的 OpenAI-compatible HTTP 适配器处理 `/v1/chat/completions`。RelayDock 不会把一种上游 API 形状自动翻译成另一种；`/v1/responses` 或 `/v1/embeddings` 只能路由到实际支持相应端点的提供商。

## 7. 测试 API 命令

`user_key` 必须是 RelayDock 签发的一次性下游 Key，不能使用上游 Key。

```bash
export RELAYDOCK_URL=https://api.your-domain.com/v1
export RELAYDOCK_API_KEY='rdk_live_replace_me'

curl -fsS "$RELAYDOCK_URL/models" \
  -H "Authorization: Bearer $RELAYDOCK_API_KEY"

curl -fsS "$RELAYDOCK_URL/chat/completions" \
  -H "Authorization: Bearer $RELAYDOCK_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"Reply exactly: RELAYDOCK_OK"}],"stream":false}'
```

Python OpenAI SDK：

```python
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["RELAYDOCK_API_KEY"],
    base_url="https://api.your-domain.com/v1",
)

response = client.chat.completions.create(
    model="deepseek-chat",
    messages=[{"role": "user", "content": "Reply exactly: RELAYDOCK_OK"}],
)
print(response.choices[0].message.content)
```

流式测试：

```bash
curl -N "$RELAYDOCK_URL/chat/completions" \
  -H "Authorization: Bearer $RELAYDOCK_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"openrouter-auto","messages":[{"role":"user","content":"Say hello"}],"stream":true}'
```

## 备份、升级和恢复

每日逻辑备份：

```bash
sudo bash /opt/relaydock/src/deploy/production/scripts/backup.sh /opt/relaydock
```

可加入 `/etc/cron.d/relaydock-backup`：

```cron
17 3 * * * root /bin/bash /opt/relaydock/src/deploy/production/scripts/backup.sh /opt/relaydock >>/var/log/relaydock-backup.log 2>&1
```

脚本保留 14 天并生成 SHA-256。还必须在 VPS 之外加密备份 `RELAYDOCK_MASTER_KEY`，否则数据库中的上游密钥无法恢复。

升级：

```bash
sudo bash /opt/relaydock/src/deploy/production/scripts/backup.sh /opt/relaydock
cd /opt/relaydock/src
git pull --ff-only
sudo bash deploy/production/scripts/prepare-host.sh /opt/relaydock /opt/relaydock/src
sudo bash deploy/production/scripts/deploy.sh /opt/relaydock
```

恢复前停止 RelayDock 写入，在隔离数据库中先验证备份：

```bash
gunzip -c data/backups/relaydock-TIMESTAMP.sql.gz \
  | sudo docker compose exec -T postgres psql -U relaydock -d relaydock
```

不要用复制正在运行的 `data/postgres` 目录代替一致性数据库备份。

## 8. 故障排查方法

基础检查：

```bash
cd /opt/relaydock
sudo docker compose config --quiet
sudo docker compose ps
sudo docker compose logs --tail 200 relaydock
sudo docker compose logs --tail 200 nginx certbot postgres redis
sudo docker compose exec -T relaydock wget -qO- http://127.0.0.1:8080/healthz
sudo docker compose exec -T relaydock wget -qO- http://127.0.0.1:8080/readyz
sudo ss -lntp | grep -E ':(80|443)\b'
df -h /opt/relaydock
free -h
```

| 现象 | 排查与处理 |
| --- | --- |
| RelayDock 立即退出 | 检查 `.env` 权限、32-byte master key、数据库/Redis URL 和管理员密码长度 |
| `readyz` 503 | 查看 PostgreSQL/Redis health 与目录 UID；Redis 满时会 `noeviction` 并让限流故障关闭 |
| Nginx 503 | HTTPS 尚未启用，或 RelayDock 未 healthy；先查 loopback `readyz` |
| HTTPS 签发失败 | 核对 A/AAAA、80 端口、云防火墙、DNS 代理和 `/.well-known/acme-challenge/` |
| 401 `invalid_api_key` | 使用 RelayDock 下游 Key，不要使用 OpenAI/DeepSeek/OpenRouter Key |
| 404 `model_not_found` | 项目没有对应别名授权，或路由被禁用 |
| 503 `provider_unavailable` | 凭据组无健康凭据、Redis 不可用，或凭据处于 cooldown/auth failed |
| 上游 401 | 在官方提供商处重新签发 Key，然后安全替换；不要轮换账号或代理绕过限制 |
| 上游 429 | 降低流量并等待提供商 reset/cooldown；Nginx 和 RelayDock 双层限流均可能返回 429 |
| SSE 一次性返回 | 确认 Nginx `proxy_buffering off`、响应为 `text/event-stream`，中间 CDN 未缓冲 |
| 502 | 检查 VPS DNS/出站 443、上游 base URL、提供商状态和 Nginx/RelayDock 日志 |
| 磁盘增长 | 检查 PostgreSQL、Nginx、RelayDock 日志和备份；Docker 日志已限制为 5×10 MB/服务 |

## 扩展到多节点

单机稳定后，扩容顺序建议：

1. 将 PostgreSQL 和 Redis 迁移到同区域托管服务或独立高可用节点。
2. 把 RelayDock 镜像推送到私有镜像仓库，多个实例共享相同 master/HMAC/JWT secrets。
3. 用云负载均衡器终止 TLS，RelayDock 实例保持无状态；迁移由 advisory lock 串行化。
4. 把 Nginx per-IP 防护上移到 WAF/边缘限流，并按真实客户端 IP 配置可信代理链。
5. 集中采集 Prometheus `/metrics`、结构化日志和告警，压测后再提高连接池或实例数。

不要在多个节点间同步本地 PostgreSQL bind directory，也不要把 Docker socket、控制平面或上游密钥暴露给公网。
