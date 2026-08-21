[CmdletBinding()]
param(
    [string]$ControlUrl = "http://127.0.0.1:8081",
    [string]$GatewayUrl = "http://127.0.0.1:8080",
    [string]$MockUrl = "http://127.0.0.1:8090",
    [string]$EnvFile = "",
    [string]$MockProviderBaseUrl = "http://mock-openai:8090/v1",
    [string]$PostgresContainer = "relaydock-postgres-1",
    [string]$PythonExecutable = "python",
    [switch]$RunSdkTests,
    [switch]$ConfirmIsolatedTestDatabase
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

if (-not $ConfirmIsolatedTestDatabase) {
    throw "Pass -ConfirmIsolatedTestDatabase only for a disposable local ModelDock deployment."
}

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
if ([string]::IsNullOrWhiteSpace($EnvFile)) {
    $EnvFile = Join-Path $repoRoot ".env"
} elseif (-not [System.IO.Path]::IsPathRooted($EnvFile)) {
    $EnvFile = Join-Path $repoRoot $EnvFile
}
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) {
    throw "The requested environment file does not exist."
}

foreach ($candidate in @($ControlUrl, $GatewayUrl, $MockUrl)) {
    $uri = [Uri]$candidate
    if ($uri.Scheme -notin @("http", "https") -or $uri.Host -notin @("127.0.0.1", "localhost", "::1")) {
        throw "The commercial baseline smoke test accepts loopback service URLs only."
    }
}

function Read-DotEnv {
    param([string]$Path)
    $settings = @{}
    foreach ($rawLine in Get-Content -LiteralPath $Path) {
        $line = $rawLine.Trim()
        if (-not $line -or $line.StartsWith("#") -or -not $line.Contains("=")) { continue }
        $name, $value = $line.Split("=", 2)
        $settings[$name.Trim()] = $value.Trim()
    }
    return $settings
}

function Require-Setting {
    param([hashtable]$Settings, [string]$Name)
    if (-not $Settings.ContainsKey($Name) -or [string]::IsNullOrWhiteSpace([string]$Settings[$Name]) -or
        ([string]$Settings[$Name]).Contains("CHANGE_ME")) {
        throw "The environment file must contain a non-placeholder $Name value."
    }
    return [string]$Settings[$Name]
}

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Assert-Equal {
    param([object]$Actual, [object]$Expected, [string]$Message)
    if ($Actual -ne $Expected) { throw "$Message (actual=$Actual expected=$Expected)" }
}

function Invoke-Control {
    param(
        [Microsoft.PowerShell.Commands.WebRequestSession]$Session,
        [string]$Csrf,
        [string]$Method,
        [string]$Path,
        [object]$Body
    )
    $headers = @{}
    if ($Method -notin @("GET", "HEAD")) { $headers["X-CSRF-Token"] = $Csrf }
    $parameters = @{
        Uri = "$ControlUrl$Path"
        Method = $Method
        WebSession = $Session
        Headers = $headers
        ContentType = "application/json"
    }
    if ($null -ne $Body) { $parameters.Body = $Body | ConvertTo-Json -Depth 16 -Compress }
    return Invoke-RestMethod @parameters
}

function Invoke-Gateway {
    param([string]$ApiKey, [string]$Path, [hashtable]$Body)
    $headers = @{
        Authorization = "Bearer $ApiKey"
        "X-Client-Request-Id" = "commercial-$([Guid]::NewGuid().ToString('N'))"
    }
    $response = Invoke-WebRequest -Uri "$GatewayUrl$Path" -Method POST -Headers $headers `
        -ContentType "application/json" -Body ($Body | ConvertTo-Json -Depth 12 -Compress) -UseBasicParsing
    return [pscustomobject]@{
        Status = [int]$response.StatusCode
        RequestID = [string]$response.Headers["X-Request-Id"]
        ContentType = [string]$response.Headers["Content-Type"]
        Body = [string]$response.Content
    }
}

function Invoke-Psql {
    param([string]$Sql)
    $output = @(& docker exec $PostgresContainer psql --no-psqlrc --tuples-only --no-align --set ON_ERROR_STOP=1 `
        --username $script:postgresUser --dbname $script:postgresDatabase --command $Sql 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "The isolated PostgreSQL verification query failed; diagnostic output was suppressed."
    }
    return ([string]::Join([Environment]::NewLine, $output)).Trim()
}

function Wait-ForTrace {
    param([string]$RequestID)
    if ($RequestID -notmatch '^rd_req_[a-zA-Z0-9]+$') {
        throw "The gateway returned an unsafe or unexpected request ID."
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        $sql = "SELECT concat_ws('|',`n" +
            "(SELECT count(*) FROM request_logs WHERE request_id='$RequestID'),`n" +
            "(SELECT count(*) FROM billing_usage_records WHERE request_id='$RequestID'),`n" +
            "(SELECT count(*) FROM wallet_transactions wt JOIN billing_usage_records b ON b.id=wt.usage_record_id WHERE b.request_id='$RequestID' AND wt.transaction_type='CHARGE'),`n" +
            "(SELECT count(*) FROM audit_logs WHERE resource_id='$RequestID' OR after_state->>'request_id'='$RequestID'));"
        $result = Invoke-Psql -Sql $sql
        $parts = @($result.Split("|"))
        if ($parts.Count -eq 4 -and [int]$parts[0] -eq 1 -and [int]$parts[1] -eq 1 -and [int]$parts[2] -eq 1) {
            return [pscustomobject]@{
                RequestLog = [int]$parts[0]
                UsageRecord = [int]$parts[1]
                WalletTransaction = [int]$parts[2]
                AuditLog = [int]$parts[3]
            }
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "The request, billing usage, and wallet charge rows did not become traceable within 15 seconds."
}

$settings = Read-DotEnv -Path $EnvFile
$adminEmail = Require-Setting $settings "RELAYDOCK_ADMIN_EMAIL"
$adminPassword = Require-Setting $settings "RELAYDOCK_ADMIN_PASSWORD"
$mockProviderKey = if ($settings.ContainsKey("MOCK_OPENAI_API_KEY")) { [string]$settings["MOCK_OPENAI_API_KEY"] } else { "mock-upstream-key" }
$mockTestToken = if ($settings.ContainsKey("MOCK_TEST_TOKEN")) { [string]$settings["MOCK_TEST_TOKEN"] } else { "relaydock-test-control" }
$script:postgresUser = Require-Setting $settings "POSTGRES_USER"
$script:postgresDatabase = Require-Setting $settings "POSTGRES_DB"

$postgresInspect = docker inspect $PostgresContainer | ConvertFrom-Json
if (@($postgresInspect).Count -ne 1 -or -not [bool]$postgresInspect[0].State.Running -or
    [string]$postgresInspect[0].Config.Labels.'com.docker.compose.service' -ne "postgres") {
    throw "The expected local Compose PostgreSQL service is not running."
}

$run = [Guid]::NewGuid().ToString("N").Substring(0, 12)
$userPassword = "Commercial!$([Guid]::NewGuid().ToString('N'))"
$chatAlias = "commercial-chat-$run"
$embeddingAlias = "commercial-embedding-$run"

$health = Invoke-RestMethod -Uri "$GatewayUrl/healthz"
$ready = Invoke-RestMethod -Uri "$ControlUrl/readyz"
Assert-Equal ([string]$health.status) "ok" "Gateway liveness failed"
Assert-Equal ([string]$ready.status) "ready" "Control-plane readiness failed"

$adminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$adminLogin = Invoke-RestMethod -Uri "$ControlUrl/api/admin/auth/login" -Method POST -WebSession $adminSession `
    -ContentType "application/json" -Body (@{ email = $adminEmail; password = $adminPassword } | ConvertTo-Json -Compress)
$adminCsrf = [string]$adminLogin.csrf_token
Assert-True (-not [string]::IsNullOrWhiteSpace($adminCsrf)) "Administrator login did not issue a CSRF token"

$provider = Invoke-Control $adminSession $adminCsrf POST "/api/admin/providers" @{
    name = "Commercial Mock $run"
    slug = "commercial-mock-$run"
    provider_type = "openai"
    base_url = $MockProviderBaseUrl
    enabled = $true
    config = @{ test_only = $true }
    commercial_status = "COMMERCIAL_APPROVED"
    commercial_resale_status = "APPROVED"
    legal_entity = "ModelDock local integration fixture"
    contract_type = "TEST_ONLY"
    contract_start_at = (Get-Date).ToUniversalTime().AddHours(-1).ToString("o")
    contract_end_at = (Get-Date).ToUniversalTime().AddDays(1).ToString("o")
    allowed_customer_regions = @("*")
    data_processing_regions = @("*")
    terms_version = "local-integration-v1"
}
$primaryGroup = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credential-groups" @{
    provider_id = $provider.id
    name = "Commercial Empty Primary $run"
    description = "Intentionally empty to verify pre-dispatch fallback"
}
$fallbackGroup = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credential-groups" @{
    provider_id = $provider.id
    name = "Commercial Fallback $run"
    description = "Deterministic local fallback credential"
}
$fallbackCredential = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credentials" @{
    provider_id = $provider.id
    name = "Commercial Fallback Credential $run"
    secret = $mockProviderKey
    group_id = $fallbackGroup.id
    validate = $true
    priority = 100
    weight = 100
    max_concurrency = 8
}
$sync = Invoke-Control $adminSession $adminCsrf POST "/api/admin/providers/$($provider.id)/sync-models" @{
    credential_id = $fallbackCredential.id
}
Assert-True ([int]$sync.synced -ge 2) "The mock provider did not synchronize both test models"

$models = @((Invoke-Control $adminSession $adminCsrf GET "/api/admin/models" $null).data)
$chatModel = $models | Where-Object { $_.provider_id -eq $provider.id -and $_.provider_model_id -eq "mock-chat" } | Select-Object -First 1
$embeddingModel = $models | Where-Object { $_.provider_id -eq $provider.id -and $_.provider_model_id -eq "mock-embedding" } | Select-Object -First 1
Assert-True ($null -ne $chatModel -and $null -ne $embeddingModel) "Synchronized model records were not found"
foreach ($model in @($chatModel, $embeddingModel)) {
    Invoke-Control $adminSession $adminCsrf POST "/api/admin/models/$($model.id)/prices" @{
        input_price = 1.0
        cached_input_price = 0.5
        output_price = 2.0
        currency = "USD"
        unit = 1000000
        source = "commercial-baseline-smoke"
    } | Out-Null
}

function New-FallbackRoute {
    param([string]$Alias, [string]$UpstreamModel)
    return Invoke-Control $adminSession $adminCsrf POST "/api/admin/model-routes" @{
        alias = $Alias
        provider_id = $provider.id
        upstream_model = $UpstreamModel
        credential_group_id = $primaryGroup.id
        fallback_group_id = $fallbackGroup.id
        routing_policy = "priority_weighted"
        fallback_config = @{}
    }
}

$chatRoute = New-FallbackRoute $chatAlias "mock-chat"
$embeddingRoute = New-FallbackRoute $embeddingAlias "mock-embedding"
$user = Invoke-Control $adminSession $adminCsrf POST "/api/admin/users" @{
    email = "commercial-$run@relayedock.local"
    password = $userPassword
    display_name = "Commercial Smoke $run"
    role = "USER"
}
$organization = Invoke-Control $adminSession $adminCsrf POST "/api/admin/organizations" @{
    name = "Commercial Organization $run"
    slug = "commercial-org-$run"
    status = "ACTIVE"
    metadata = @{ test_run = $run }
}
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/organizations/$($organization.id)/members/$($user.id)" @{
    role = "MEMBER"
    status = "ACTIVE"
} | Out-Null
$project = Invoke-Control $adminSession $adminCsrf POST "/api/admin/organizations/$($organization.id)/projects" @{
    name = "Commercial Project $run"
    slug = "commercial-project-$run"
    description = "Commercial baseline smoke test"
    status = "ACTIVE"
    metadata = @{ test_run = $run }
}
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($project.id)/members/$($user.id)" @{
    role = "DEVELOPER"
    status = "ACTIVE"
} | Out-Null
foreach ($route in @($chatRoute, $embeddingRoute)) {
    Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($project.id)/routes" @{
        model_route_id = $route.id
        enabled = $true
        routing_config = @{}
    } | Out-Null
}

$wallets = @((Invoke-Control $adminSession $adminCsrf GET "/api/admin/wallets" $null).data)
$wallet = $wallets | Where-Object organization_id -eq $organization.id | Select-Object -First 1
Assert-True ($null -ne $wallet) "Organization creation did not create a wallet"
$concurrentIdempotencyKey = "commercial-concurrent-topup-$run"
$concurrentJob = {
    param($ControlUrl, $AdminEmail, $AdminPassword, $WalletID, $IdempotencyKey)
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $login = Invoke-RestMethod -Uri "$ControlUrl/api/admin/auth/login" -Method POST -WebSession $session `
        -ContentType "application/json" -Body (@{ email = $AdminEmail; password = $AdminPassword } | ConvertTo-Json -Compress)
    try {
        $response = Invoke-WebRequest -Uri "$ControlUrl/api/admin/wallets/$WalletID/topups" -Method POST `
            -WebSession $session -Headers @{ "X-CSRF-Token" = [string]$login.csrf_token } -ContentType "application/json" `
            -Body (@{ amount = 1.25; idempotency_key = $IdempotencyKey; reference = "commercial-concurrency-smoke" } | ConvertTo-Json -Compress) `
            -UseBasicParsing
        return [int]$response.StatusCode
    } catch {
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
        throw
    }
}
$jobs = @()
try {
    $jobs = @(1..8 | ForEach-Object {
        Start-Job -ScriptBlock $concurrentJob -ArgumentList $ControlUrl, $adminEmail, $adminPassword, $wallet.id, $concurrentIdempotencyKey
    })
    $completedJobs = @($jobs | Wait-Job -Timeout 45)
    Assert-Equal $completedJobs.Count $jobs.Count "Concurrent wallet requests did not all finish within 45 seconds"
    $concurrentTopupStatuses = @($jobs | Receive-Job | ForEach-Object { [int]$_ })
} finally {
    $jobs | Stop-Job -ErrorAction SilentlyContinue
    $jobs | Remove-Job -Force -ErrorAction SilentlyContinue
}
$concurrentLedgerCount = Invoke-Psql -Sql "SELECT count(*) FROM wallet_transactions WHERE wallet_id='$($wallet.id)' AND idempotency_key='$concurrentIdempotencyKey';"
Assert-Equal ([int]$concurrentLedgerCount) 1 "Concurrent idempotent top-ups created more than one wallet ledger row"
Assert-True (@($concurrentTopupStatuses | Where-Object { $_ -in @(201, 409) }).Count -eq 8) "Concurrent top-up responses contained an unexpected status"
$topupIdempotencyKey = "commercial-topup-$run"
$topupBody = @{ amount = 10.00; idempotency_key = $topupIdempotencyKey; reference = "commercial-baseline-smoke" }
$topup = Invoke-Control $adminSession $adminCsrf POST "/api/admin/wallets/$($wallet.id)/topups" $topupBody
$topupReplay = Invoke-Control $adminSession $adminCsrf POST "/api/admin/wallets/$($wallet.id)/topups" $topupBody
Assert-Equal ([string]$topup.id) ([string]$topupReplay.id) "Sequential wallet idempotency replay returned a different transaction"
$wallet = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/wallets/$($wallet.id)" @{
    billing_mode = "PREPAID"
    credit_limit = 0
    status = "ACTIVE"
}

$userSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$userLogin = Invoke-RestMethod -Uri "$ControlUrl/api/console/auth/login" -Method POST -WebSession $userSession `
    -ContentType "application/json" -Body (@{ email = $user.email; password = $userPassword } | ConvertTo-Json -Compress)
$userCsrf = [string]$userLogin.csrf_token
$keyResponse = Invoke-Control $userSession $userCsrf POST "/api/console/api-keys" @{
    project_id = $project.id
    name = "Commercial smoke key $run"
    environment = "test"
    rate_limit_rpm = 120
    rate_limit_tpm = 200000
    allowed_models = @($chatAlias, $embeddingAlias)
}
$apiKey = [string]$keyResponse.key
Assert-True ($apiKey.StartsWith("rdk_test_")) "The console did not return a one-time rdk_test API key"

Invoke-RestMethod -Uri "$MockUrl/__test/reset" -Method POST -Headers @{ "X-RelayDock-Test-Token" = $mockTestToken } `
    -ContentType "application/json" -Body "{}" | Out-Null
$normal = Invoke-Gateway $apiKey "/v1/responses" @{ model = $chatAlias; input = "Commercial baseline normal request" }
$stream = Invoke-Gateway $apiKey "/v1/responses" @{ model = $chatAlias; input = "Commercial baseline stream request"; stream = $true }
Assert-Equal $normal.Status 200 "The ordinary OpenAI-compatible request failed"
Assert-Equal $stream.Status 200 "The streaming OpenAI-compatible request failed"
Assert-True ($stream.ContentType -like "text/event-stream*") "The streaming response did not use text/event-stream"
Assert-True ($stream.Body.Contains("response.completed")) "The streaming response did not contain response.completed"

$mockRequests = @((Invoke-RestMethod -Uri "$MockUrl/__test/requests" -Headers @{ "X-RelayDock-Test-Token" = $mockTestToken }).data)
Assert-Equal $mockRequests.Count 2 "The two baseline calls did not reach the mock provider exactly twice"
Assert-True ([bool]$mockRequests[1].stream) "The upstream did not receive the streaming flag"

$logs = @((Invoke-Control $adminSession $adminCsrf GET "/api/admin/request-logs?limit=500" $null).data)
$normalLog = $logs | Where-Object request_id -eq $normal.RequestID | Select-Object -First 1
$streamLog = $logs | Where-Object request_id -eq $stream.RequestID | Select-Object -First 1
Assert-True ($null -ne $normalLog -and $null -ne $streamLog) "The baseline requests were not written to request_logs"
Assert-Equal ([string]$normalLog.credential_id) ([string]$fallbackCredential.id) "The empty primary group did not fall back to the configured fallback credential"
Assert-True ([int64]$normalLog.total_tokens -gt 0 -and [double]$normalLog.estimated_cost -gt 0) "Normal request usage or cost was not recorded"
Assert-True ([int64]$streamLog.total_tokens -gt 0 -and [double]$streamLog.estimated_cost -gt 0) "Streaming request usage or cost was not recorded"

$normalTrace = Wait-ForTrace $normal.RequestID
$streamTrace = Wait-ForTrace $stream.RequestID
$walletTransactions = @((Invoke-Control $adminSession $adminCsrf GET "/api/admin/wallets/$($wallet.id)/transactions?limit=100" $null).data)
$topupRows = @($walletTransactions | Where-Object idempotency_key -eq $topupIdempotencyKey)
$chargeRows = @($walletTransactions | Where-Object { $_.transaction_type -eq "CHARGE" -and $_.reference -in @("usage:$($normal.RequestID)", "usage:$($stream.RequestID)") })
Assert-Equal $topupRows.Count 1 "The idempotent top-up produced more than one wallet ledger row"
Assert-Equal $chargeRows.Count 2 "The two successful priced requests did not produce two wallet charges"

$sdkExit = $null
if ($RunSdkTests) {
    $previous = @{
        Key = $env:RELAYDOCK_API_KEY
        Base = $env:RELAYDOCK_BASE_URL
        Chat = $env:RELAYDOCK_CHAT_MODEL
        Embedding = $env:RELAYDOCK_EMBEDDING_MODEL
    }
    try {
        $env:RELAYDOCK_API_KEY = $apiKey
        $env:RELAYDOCK_BASE_URL = "$GatewayUrl/v1"
        $env:RELAYDOCK_CHAT_MODEL = $chatAlias
        $env:RELAYDOCK_EMBEDDING_MODEL = $embeddingAlias
        & $PythonExecutable -m pytest -q (Join-Path $repoRoot "tests\sdk\python")
        $sdkExit = $LASTEXITCODE
    } finally {
        $env:RELAYDOCK_API_KEY = $previous.Key
        $env:RELAYDOCK_BASE_URL = $previous.Base
        $env:RELAYDOCK_CHAT_MODEL = $previous.Chat
        $env:RELAYDOCK_EMBEDDING_MODEL = $previous.Embedding
    }
}

$auditTraceComplete = $normalTrace.AuditLog -gt 0 -and $streamTrace.AuditLog -gt 0
$result = [ordered]@{
    health = "PASS"
    admin_login = "PASS"
    admin_created_user = "PASS"
    organization_and_project = "PASS"
    api_key_created = "PASS"
    provider_and_models = "PASS"
    ordinary_request = "PASS"
    streaming_request = "PASS"
    provider_fallback = "PASS"
    usage_written = "PASS"
    wallet_topup_idempotency = "PASS"
    wallet_topup_concurrency = "PASS"
    wallet_topup_concurrency_statuses = @($concurrentTopupStatuses)
    wallet_charges = "PASS"
    normal_request_id = $normal.RequestID
    stream_request_id = $stream.RequestID
    normal_trace = $normalTrace
    stream_trace = $streamTrace
    audit_trace = if ($auditTraceComplete) { "PASS" } else { "FAIL" }
    sdk_tests = if ($null -eq $sdkExit) { "NOT_RUN" } elseif ($sdkExit -eq 0) { "PASS" } else { "FAIL" }
}
$result | ConvertTo-Json -Depth 8

if ($null -ne $sdkExit -and $sdkExit -ne 0) {
    throw "The official Python SDK compatibility suite failed."
}
if (-not $auditTraceComplete) {
    throw "Inference request IDs do not link to audit_logs; the commercial traceability acceptance criterion is not met."
}
