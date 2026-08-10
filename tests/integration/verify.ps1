[CmdletBinding()]
param(
    [string]$ControlUrl = "http://127.0.0.1:8081",
    [string]$GatewayUrl = "http://127.0.0.1:8080",
    [string]$AdminEmail = $env:RELAYDOCK_ADMIN_EMAIL,
    [string]$AdminPassword = $env:RELAYDOCK_ADMIN_PASSWORD,
    [string]$TestUserEmail = "sdk-user@relayedock.local",
    [string]$TestUserPassword = $env:RELAYDOCK_TEST_USER_PASSWORD,
    [string]$TestProviderSlug = "relayedock-integration-mock",
    [string]$MockProviderBaseUrl = "http://mock-openai:8090/v1",
    [string]$MockProviderKey = "mock-upstream-key",
    [string]$SdkKeyOutput = "",
    [switch]$ConfirmIsolatedTestDatabase
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($AdminEmail)) {
    $AdminEmail = "admin@relayedock.local"
}
if ([string]::IsNullOrWhiteSpace($AdminPassword)) {
    throw "Set RELAYDOCK_ADMIN_PASSWORD or pass -AdminPassword. This script has no built-in administrator password."
}
if ([string]::IsNullOrWhiteSpace($TestUserPassword)) {
    throw "Set RELAYDOCK_TEST_USER_PASSWORD or pass -TestUserPassword. Use only an isolated, disposable test deployment."
}
if (-not $ConfirmIsolatedTestDatabase) {
    throw "Pass -ConfirmIsolatedTestDatabase after verifying that this is a disposable local test database."
}
$controlUri = [Uri]$ControlUrl
if ($controlUri.Scheme -notin @("http", "https") -or $controlUri.Host -notin @("127.0.0.1", "localhost", "::1")) {
    throw "The integration runner only accepts a loopback ControlUrl."
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
    if ($null -ne $Body) { $parameters.Body = ($Body | ConvertTo-Json -Depth 12 -Compress) }
    Invoke-RestMethod @parameters
}

function Invoke-Status {
    param(
        [string]$Method,
        [string]$Uri,
        [string]$ApiKey,
        [object]$Body,
        [Microsoft.PowerShell.Commands.WebRequestSession]$Session,
        [hashtable]$Headers = @{}
    )
    if ($ApiKey) { $Headers["Authorization"] = "Bearer $ApiKey" }
    $parameters = @{ Uri = $Uri; Method = $Method; Headers = $Headers; UseBasicParsing = $true }
    if ($Session) { $parameters.WebSession = $Session }
    if ($null -ne $Body) {
        $parameters.ContentType = "application/json"
        $parameters.Body = ($Body | ConvertTo-Json -Depth 12 -Compress)
    }
    try {
        $response = Invoke-WebRequest @parameters
        return [int]$response.StatusCode
    } catch {
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
        throw
    }
}

$adminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$login = Invoke-RestMethod -Uri "$ControlUrl/api/admin/auth/login" -Method POST -WebSession $adminSession -ContentType "application/json" -Body (@{ email = $AdminEmail; password = $AdminPassword } | ConvertTo-Json -Compress)
$adminCsrf = [string]$login.csrf_token

$providers = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/providers" $null).data
$officialProvider = $providers | Where-Object slug -eq "openai" | Select-Object -First 1
if (-not $officialProvider) { throw "The built-in OpenAI provider was not found." }
$provider = $providers | Where-Object slug -eq $TestProviderSlug | Select-Object -First 1
if (-not $provider) {
    $provider = Invoke-Control $adminSession $adminCsrf POST "/api/admin/providers" @{
        name = "RelayDock Integration Mock"
        slug = $TestProviderSlug
        provider_type = "openai"
        base_url = $MockProviderBaseUrl
        enabled = $true
        config = @{}
    }
} else {
    $provider = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/providers/$($provider.id)" @{
        name = "RelayDock Integration Mock"
        slug = $TestProviderSlug
        provider_type = "openai"
        base_url = $MockProviderBaseUrl
        enabled = $true
        config = @{}
    }
}

$groups = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/credential-groups" $null).data
$group = $groups | Where-Object { $_.name -eq "RelayDock Integration Pool" -and $_.provider_id -eq $provider.id } | Select-Object -First 1
if (-not $group) {
    $group = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credential-groups" @{ provider_id = $provider.id; name = "RelayDock Integration Pool"; description = "Deterministic local SDK verification only" }
}

$credentials = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/credentials?limit=100" $null).data
$credential = $credentials | Where-Object { $_.name -eq "RelayDock Integration Credential" -and $_.provider_id -eq $provider.id } | Select-Object -First 1
if (-not $credential) {
    $import = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credentials/import" @{
        validate = $true
        credentials = @(@{ name = "RelayDock Integration Credential"; api_key = $MockProviderKey; provider_id = $provider.id; group_id = $group.id; priority = 10; weight = 100; max_concurrency = 8 })
    }
    if ($import.created -ne 1) { throw "Authorized credential import failed: $($import | ConvertTo-Json -Depth 8 -Compress)" }
    $credentialId = [string]$import.results[0].id
} else {
    $credentialId = [string]$credential.id
}

$sync = Invoke-Control $adminSession $adminCsrf POST "/api/admin/providers/$($provider.id)/sync-models" @{ credential_id = $credentialId }
if ($sync.synced -lt 2) { throw "Expected at least two mock models." }

$models = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/models" $null).data
foreach ($upstream in @("mock-chat", "mock-embedding")) {
    $model = $models | Where-Object { $_.provider_model_id -eq $upstream -and $_.provider_id -eq $provider.id } | Select-Object -First 1
    if (-not $model) { throw "Synced model $upstream was not found." }
    $prices = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/models/$($model.id)/prices" $null).data
    if (-not $prices) {
        Invoke-Control $adminSession $adminCsrf POST "/api/admin/models/$($model.id)/prices" @{ input_price = 1.0; cached_input_price = 0.5; output_price = 2.0; currency = "USD"; unit = 1000000; source = "integration-test" } | Out-Null
    }
}

$routes = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/model-routes" $null).data
$routeDefinitions = @(
    @{ alias = "relayedock-integration-chat"; upstream_model = "mock-chat" },
    @{ alias = "relayedock-integration-embedding"; upstream_model = "mock-embedding" }
)
foreach ($definition in $routeDefinitions) {
    $existingRoute = $routes | Where-Object alias -eq $definition.alias | Select-Object -First 1
    if (-not $existingRoute) {
        Invoke-Control $adminSession $adminCsrf POST "/api/admin/model-routes" @{ alias = $definition.alias; provider_id = $provider.id; upstream_model = $definition.upstream_model; credential_group_id = $group.id; routing_policy = "priority_weighted" } | Out-Null
    } elseif ($existingRoute.provider_id -ne $provider.id) {
        throw "The reserved integration alias '$($definition.alias)' belongs to another provider. Use a disposable test database."
    }
}

$users = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/users?limit=100" $null).data
$user = $users | Where-Object email -eq $TestUserEmail | Select-Object -First 1
if (-not $user) {
    $user = Invoke-Control $adminSession $adminCsrf POST "/api/admin/users" @{ email = $TestUserEmail; password = $TestUserPassword; display_name = "SDK Verification User"; role = "USER" }
}

$userSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$userLogin = Invoke-RestMethod -Uri "$ControlUrl/api/console/auth/login" -Method POST -WebSession $userSession -ContentType "application/json" -Body (@{ email = $user.email; password = $TestUserPassword } | ConvertTo-Json -Compress)
$userCsrf = [string]$userLogin.csrf_token
$expiresAt = [DateTimeOffset]::UtcNow.AddHours(2).ToString("o")
$keyResponse = Invoke-Control $userSession $userCsrf POST "/api/console/api-keys" @{ name = "Official SDK integration"; environment = "test"; expires_at = $expiresAt; rate_limit_rpm = 60; rate_limit_tpm = 100000; allowed_models = @("relayedock-integration-chat", "relayedock-integration-embedding") }
$sdkKey = [string]$keyResponse.key
if (-not $sdkKey.StartsWith("rdk_test_")) { throw "RelayDock did not return the expected one-time test key." }

$temporaryKeys = [System.Collections.Generic.List[object]]::new()
$temporaryGroupIDs = [System.Collections.Generic.List[string]]::new()
$temporaryRouteAliases = [System.Collections.Generic.List[string]]::new()
if ([string]::IsNullOrWhiteSpace($SdkKeyOutput)) {
    $temporaryKeys.Add($keyResponse)
}

try {
$guardGroupName = "RelayDock Provider Guard $([Guid]::NewGuid().ToString('N'))"
$guardGroup = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credential-groups" @{ provider_id = $officialProvider.id; name = $guardGroupName; description = "Temporary cross-provider integrity assertion" }
$temporaryGroupIDs.Add([string]$guardGroup.id)
$memberProviderMismatchStatus = Invoke-Status PUT "$ControlUrl/api/admin/credential-groups/$($guardGroup.id)/members/$credentialId" "" @{ weight = 100; priority = 0 } $adminSession @{ "X-CSRF-Token" = $adminCsrf }

$guardRouteAlias = "relaydock-provider-guard-$([Guid]::NewGuid().ToString('N'))"
$temporaryRouteAliases.Add($guardRouteAlias)
$routeProviderMismatchStatus = Invoke-Status POST "$ControlUrl/api/admin/model-routes" "" @{ alias = $guardRouteAlias; provider_id = $officialProvider.id; upstream_model = "mock-chat"; credential_group_id = $group.id; routing_policy = "least_loaded" } $adminSession @{ "X-CSRF-Token" = $adminCsrf }

$invalidKeyStatus = Invoke-Status GET "$GatewayUrl/v1/models" "rdk_test_invalid" $null $null
$modelsStatus = Invoke-Status GET "$GatewayUrl/v1/models" $sdkKey $null $null
$responseStatus = Invoke-Status POST "$GatewayUrl/v1/responses" $sdkKey @{ model = "relayedock-integration-chat"; input = "RelayDock integration test" } $null @{ "X-Client-Request-Id" = "relayedock-integration-client-id" }

$restricted = Invoke-Control $userSession $userCsrf POST "/api/console/api-keys" @{ name = "Restricted integration"; environment = "test"; expires_at = $expiresAt; rate_limit_rpm = 60; rate_limit_tpm = 100000; allowed_models = @("relayedock-integration-chat") }
$temporaryKeys.Add($restricted)
$forbiddenModelStatus = Invoke-Status POST "$GatewayUrl/v1/embeddings" ([string]$restricted.key) @{ model = "relayedock-integration-embedding"; input = "forbidden" } $null

$limited = Invoke-Control $userSession $userCsrf POST "/api/console/api-keys" @{ name = "RPM integration"; environment = "test"; expires_at = $expiresAt; rate_limit_rpm = 1; rate_limit_tpm = 100000; allowed_models = @("relayedock-integration-chat") }
$temporaryKeys.Add($limited)
$rateFirst = Invoke-Status GET "$GatewayUrl/v1/models" ([string]$limited.key) $null $null
$rateSecond = Invoke-Status GET "$GatewayUrl/v1/models" ([string]$limited.key) $null $null

$quota = Invoke-Control $userSession $userCsrf POST "/api/console/api-keys" @{ name = "Quota integration"; environment = "test"; expires_at = $expiresAt; rate_limit_rpm = 60; rate_limit_tpm = 100000; monthly_token_limit = 1; allowed_models = @("relayedock-integration-chat") }
$temporaryKeys.Add($quota)
$quotaStatus = Invoke-Status POST "$GatewayUrl/v1/responses" ([string]$quota.key) @{ model = "relayedock-integration-chat"; input = "quota should reject this request before upstream" } $null
$crossRealmCookieStatus = Invoke-Status GET "$ControlUrl/api/admin/providers" "" $null $userSession
$consoleSessionCookie = $userSession.Cookies.GetCookies([Uri]$ControlUrl) | Where-Object Name -eq "relayedock_console_session" | Select-Object -First 1
if (-not $consoleSessionCookie) { throw "Console session cookie was not issued." }
$userBearerAdminStatus = Invoke-Status GET "$ControlUrl/api/admin/providers" ([string]$consoleSessionCookie.Value) $null $null

$badGroup = $groups | Where-Object { $_.name -eq "RelayDock Integration Invalid Pool" -and $_.provider_id -eq $provider.id } | Select-Object -First 1
if (-not $badGroup) {
    $badGroup = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credential-groups" @{ provider_id = $provider.id; name = "RelayDock Integration Invalid Pool"; description = "401 lifecycle verification only" }
}
$credentials = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/credentials?limit=100" $null).data
$badCredential = $credentials | Where-Object { $_.name -eq "RelayDock Integration Invalid Credential" -and $_.provider_id -eq $provider.id } | Select-Object -First 1
if (-not $badCredential) {
    $badCredential = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credentials" @{ provider_id = $provider.id; name = "RelayDock Integration Invalid Credential"; secret = "wrong-upstream-key"; group_id = $badGroup.id; validate = $false; priority = 10; weight = 100; max_concurrency = 2 }
}
$routes = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/model-routes" $null).data
if (-not ($routes | Where-Object alias -eq "relayedock-integration-auth-failure")) {
    Invoke-Control $adminSession $adminCsrf POST "/api/admin/model-routes" @{ alias = "relayedock-integration-auth-failure"; provider_id = $provider.id; upstream_model = "mock-chat"; credential_group_id = $badGroup.id; routing_policy = "least_loaded" } | Out-Null
}
Invoke-Control $adminSession $adminCsrf PATCH "/api/admin/credentials/$($badCredential.id)/status" @{ status = "ACTIVE" } | Out-Null
$authKey = Invoke-Control $userSession $userCsrf POST "/api/console/api-keys" @{ name = "Auth lifecycle integration"; environment = "test"; expires_at = $expiresAt; rate_limit_rpm = 60; rate_limit_tpm = 100000; allowed_models = @("relayedock-integration-auth-failure") }
$temporaryKeys.Add($authKey)
$authFailureStatus = Invoke-Status POST "$GatewayUrl/v1/responses" ([string]$authKey.key) @{ model = "relayedock-integration-auth-failure"; input = "trigger upstream 401" } $null
$credentialsAfter401 = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/credentials?limit=100" $null).data
$badCredentialStatus = [string](($credentialsAfter401 | Where-Object id -eq $badCredential.id | Select-Object -First 1).status)

$credentialJson = $credentialsAfter401 | ConvertTo-Json -Depth 10 -Compress
$secretLeak = $credentialJson.Contains($MockProviderKey) -or $credentialJson.Contains("wrong-upstream-key") -or $credentialJson.Contains("encrypted_secret")

Start-Sleep -Milliseconds 500
$requestLogs = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/request-logs?limit=100" $null).data
$successfulLog = $requestLogs | Where-Object { $_.requested_model -eq "relayedock-integration-chat" -and $_.status_code -eq 200 } | Select-Object -First 1

if (-not [string]::IsNullOrWhiteSpace($SdkKeyOutput)) {
    $outputDirectory = Split-Path -Parent $SdkKeyOutput
    if ($outputDirectory) { New-Item -ItemType Directory -Force $outputDirectory | Out-Null }
    [System.IO.File]::WriteAllText((Join-Path (Get-Location) $SdkKeyOutput), $sdkKey)
}

$summary = [ordered]@{
    admin_login = 200
    credential_imported_and_validated = $true
    models_synced = [int]$sync.synced
    invalid_key_status = $invalidKeyStatus
    models_status = $modelsStatus
    responses_status = $responseStatus
    model_forbidden_status = $forbiddenModelStatus
    rate_limit_statuses = @($rateFirst, $rateSecond)
    quota_status = $quotaStatus
    cross_realm_cookie_status = $crossRealmCookieStatus
    user_bearer_to_admin_status = $userBearerAdminStatus
    member_provider_mismatch_status = $memberProviderMismatchStatus
    route_provider_mismatch_status = $routeProviderMismatchStatus
    upstream_auth_failure_status = $authFailureStatus
    invalid_credential_lifecycle_status = $badCredentialStatus
    secret_leak_detected = $secretLeak
    request_log_recorded = [bool]$successfulLog
    logged_total_tokens = if ($successfulLog) { [int64]$successfulLog.total_tokens } else { 0 }
    logged_estimated_cost = if ($successfulLog) { [double]$successfulLog.estimated_cost } else { 0 }
    sdk_key_prefix = $sdkKey.Substring(0, [Math]::Min(18, $sdkKey.Length))
    sdk_key_file = $SdkKeyOutput
    sdk_chat_model = "relayedock-integration-chat"
    sdk_embedding_model = "relayedock-integration-embedding"
}
$summary | ConvertTo-Json -Depth 6

if ($invalidKeyStatus -ne 401 -or $modelsStatus -ne 200 -or $responseStatus -ne 200 -or $forbiddenModelStatus -ne 403 -or $rateSecond -ne 429 -or $quotaStatus -ne 403 -or $crossRealmCookieStatus -ne 401 -or $userBearerAdminStatus -ne 403 -or $memberProviderMismatchStatus -ne 422 -or $routeProviderMismatchStatus -ne 422 -or $authFailureStatus -ne 401 -or $badCredentialStatus -ne "AUTH_FAILED" -or $secretLeak -or -not $successfulLog) {
    throw "One or more RelayDock integration assertions failed."
}
} finally {
    if ($temporaryRouteAliases.Count -gt 0) {
        try {
            $currentRoutes = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/model-routes" $null).data
            foreach ($routeAlias in $temporaryRouteAliases) {
                foreach ($temporaryRoute in @($currentRoutes | Where-Object alias -eq $routeAlias)) {
                    Invoke-Control $adminSession $adminCsrf DELETE "/api/admin/model-routes/$($temporaryRoute.id)" $null | Out-Null
                }
            }
        } catch {
            Write-Warning "Could not remove one or more temporary provider-integrity routes."
        }
    }
    foreach ($temporaryGroupID in $temporaryGroupIDs) {
        try {
            Invoke-Control $adminSession $adminCsrf DELETE "/api/admin/credential-groups/$temporaryGroupID" $null | Out-Null
        } catch {
            Write-Warning "Could not remove temporary provider-integrity group $temporaryGroupID."
        }
    }
    foreach ($createdKey in $temporaryKeys) {
        $createdKeyId = [string]$createdKey.api_key.id
        if ([string]::IsNullOrWhiteSpace($createdKeyId)) { continue }
        try {
            Invoke-Control $userSession $userCsrf DELETE "/api/console/api-keys/$createdKeyId" $null | Out-Null
        } catch {
            Write-Warning "Could not revoke temporary integration key $createdKeyId."
        }
    }
}
