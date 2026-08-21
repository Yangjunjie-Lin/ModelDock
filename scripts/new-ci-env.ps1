[CmdletBinding()]
param(
    [string]$OutputPath = ".cache/ci.env"
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$target = if ([System.IO.Path]::IsPathRooted($OutputPath)) {
    [System.IO.Path]::GetFullPath($OutputPath)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $repoRoot $OutputPath))
}
if (-not $target.StartsWith($repoRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "CI environment output must remain inside the repository workspace."
}

function New-RandomBytes {
    param([int]$Length)
    $bytes = New-Object byte[] $Length
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
        return $bytes
    } finally {
        $generator.Dispose()
    }
}

function ConvertTo-Hex {
    param([byte[]]$Bytes)
    return -join ($Bytes | ForEach-Object { $_.ToString("x2") })
}

$postgresPassword = ConvertTo-Hex (New-RandomBytes 24)
$redisPassword = ConvertTo-Hex (New-RandomBytes 24)
$hmacSecret = ConvertTo-Hex (New-RandomBytes 32)
$jwtSecret = ConvertTo-Hex (New-RandomBytes 32)
$adminPassword = ConvertTo-Hex (New-RandomBytes 24)
$masterKey = [Convert]::ToBase64String((New-RandomBytes 32))
$mockKey = "mock-" + (ConvertTo-Hex (New-RandomBytes 16))
$mockWebhook = "mock-" + (ConvertTo-Hex (New-RandomBytes 16))
$mockToken = "mock-" + (ConvertTo-Hex (New-RandomBytes 16))

$contents = @"
COMPOSE_PROJECT_NAME=relaydock
RELAYDOCK_IMAGE_TAG=ci
RELAYDOCK_SOURCE_DIR=.
MODELDOCK_SERVER_IMAGE=relaydock/server
MODELDOCK_ADMIN_WEB_IMAGE=relaydock/admin-web
MODELDOCK_CONSOLE_WEB_IMAGE=relaydock/console-web
TZ=UTC
API_DOMAIN=ci.example.invalid
LETSENCRYPT_EMAIL=ci@example.invalid
ALLOWED_ORIGINS=https://ci.example.invalid
TRUSTED_PROXIES=127.0.0.1/32
GO_MODULE_PROXY=https://proxy.golang.org,direct
POSTGRES_DB=relaydock
POSTGRES_USER=relaydock
POSTGRES_PASSWORD=$postgresPassword
DATABASE_URL=postgres://relaydock:$postgresPassword@postgres:5432/relaydock?sslmode=disable
POSTGRES_HOST_PORT=55433
POSTGRES_MAX_CONNS=10
POSTGRES_MIN_CONNS=1
POSTGRES_MAX_CONN_IDLE_TIME=1m
POSTGRES_MAX_CONN_LIFETIME=5m
REDIS_PASSWORD=$redisPassword
REDIS_URL=redis://:$redisPassword@redis:6379/0
REDIS_HOST_PORT=56379
REDIS_POOL_SIZE=10
REDIS_MIN_IDLE_CONNS=1
REDIS_DIAL_TIMEOUT=5s
REDIS_READ_TIMEOUT=3s
REDIS_WRITE_TIMEOUT=3s
RELAYDOCK_MASTER_KEY=$masterKey
RELAYDOCK_API_KEY_HMAC_SECRET=$hmacSecret
RELAYDOCK_JWT_SECRET=$jwtSecret
RELAYDOCK_JWT_LIFETIME=15m
RELAYDOCK_JWT_REFRESH_LIFETIME=1h
RELAYDOCK_PROVIDER_ALLOWED_HOSTS=api.openai.com,api.deepseek.com,openrouter.ai,api.anthropic.com,generativelanguage.googleapis.com,open.bigmodel.cn,api.moonshot.cn,dashscope.aliyuncs.com,mock-openai
RELAYDOCK_PROVIDER_ALLOW_PRIVATE_NETWORK=true
RELAYDOCK_PROVIDER_ALLOW_HTTP=true
RELAYDOCK_ADMIN_EMAIL=ci-admin@example.invalid
RELAYDOCK_ADMIN_PASSWORD=$adminPassword
RELAYDOCK_ADMIN_DISPLAY_NAME=CI Administrator
RELAYDOCK_PUBLIC_CONSOLE_URL=https://ci.example.invalid
RELAYDOCK_MAIL_PROVIDER=smtp
RELAYDOCK_MAIL_FROM=ci@example.invalid
RELAYDOCK_SMTP_HOST=smtp.example.invalid
RELAYDOCK_SMTP_PORT=587
RELAYDOCK_PROVIDER_ALLOWED_HOSTS=api.openai.com,api.deepseek.com,openrouter.ai,api.anthropic.com,generativelanguage.googleapis.com,open.bigmodel.cn,api.moonshot.cn,dashscope.aliyuncs.com
RELAYDOCK_REPLICAS=2
GATEWAY_HOST=127.0.0.1
GATEWAY_PORT=58080
GATEWAY_DIAGNOSTIC_PORT=58080
CONTROL_PLANE_HOST=127.0.0.1
CONTROL_PLANE_PORT=58081
ADMIN_WEB_HOST=127.0.0.1
ADMIN_WEB_PORT=53000
CONSOLE_WEB_HOST=127.0.0.1
CONSOLE_WEB_PORT=53001
COOKIE_SECURE=false
LOG_LEVEL=info
LOG_DIR=/app/logs
MAX_REQUEST_BODY_BYTES=10485760
CREDENTIAL_COOLDOWN=30s
SHUTDOWN_TIMEOUT=10s
WEBHOOK_ALLOW_HTTP=false
WEBHOOK_ALLOW_PRIVATE_NETWORK=false
WEBHOOK_TIMEOUT=5s
WEBHOOK_POLL_INTERVAL=1s
WEBHOOK_MAX_ATTEMPTS=3
MOCK_OPENAI_PORT=58090
MOCK_OPENAI_API_KEY=$mockKey
MOCK_WEBHOOK_SECRET=$mockWebhook
MOCK_TEST_TOKEN=$mockToken
"@

$parent = [System.IO.Path]::GetDirectoryName($target)
[System.IO.Directory]::CreateDirectory($parent) | Out-Null
[System.IO.File]::WriteAllText($target, $contents, (New-Object System.Text.UTF8Encoding($false)))
Write-Host "Created an ephemeral CI environment file at $target (values suppressed)."
