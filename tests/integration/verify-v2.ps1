[CmdletBinding()]
param(
    [string]$ControlUrl = "http://127.0.0.1:8081",
    [string]$GatewayUrl = "http://127.0.0.1:13080",
    [string]$MockUrl = "http://127.0.0.1:13090",
    [string]$AdminEmail = $env:RELAYDOCK_ADMIN_EMAIL,
    [string]$AdminPassword = $env:RELAYDOCK_ADMIN_PASSWORD,
    [string]$MockProviderBaseUrl = "http://mock-openai:8090/v1",
    [string]$MockProviderKey = "mock-upstream-key",
    [string]$MockWebhookSecret = $env:MOCK_WEBHOOK_SECRET,
    [string]$MockTestToken = $env:MOCK_TEST_TOKEN,
    [switch]$ConfirmIsolatedTestDatabase
)

$ErrorActionPreference = "Stop"

if (-not $ConfirmIsolatedTestDatabase) {
    throw "Pass -ConfirmIsolatedTestDatabase only for a disposable local RelayDock deployment."
}
foreach ($candidate in @($ControlUrl, $GatewayUrl, $MockUrl)) {
    $uri = [Uri]$candidate
    if ($uri.Scheme -notin @("http", "https") -or $uri.Host -notin @("127.0.0.1", "localhost", "::1")) {
        throw "The V2 integration runner accepts loopback service URLs only."
    }
}
if ([string]::IsNullOrWhiteSpace($AdminEmail)) { $AdminEmail = "admin@relayedock.local" }
if ([string]::IsNullOrWhiteSpace($AdminPassword)) { throw "Set RELAYDOCK_ADMIN_PASSWORD or pass -AdminPassword." }
if ([string]::IsNullOrWhiteSpace($MockWebhookSecret)) { $MockWebhookSecret = "mock-webhook-secret-2026" }
if ([string]::IsNullOrWhiteSpace($MockTestToken)) { $MockTestToken = "relaydock-test-control" }

function Assert-Equal {
    param([object]$Actual, [object]$Expected, [string]$Message)
    if ($Actual -ne $Expected) { throw "$Message (actual=$Actual expected=$Expected)" }
}

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
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
    if ($null -ne $Body) { $parameters.Body = ($Body | ConvertTo-Json -Depth 16 -Compress) }
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
    $requestHeaders = @{}
    foreach ($name in $Headers.Keys) { $requestHeaders[$name] = $Headers[$name] }
    if ($ApiKey) { $requestHeaders["Authorization"] = "Bearer $ApiKey" }
    $parameters = @{ Uri = $Uri; Method = $Method; Headers = $requestHeaders; UseBasicParsing = $true }
    if ($Session) { $parameters.WebSession = $Session }
    if ($null -ne $Body) {
        $parameters.ContentType = "application/json"
        $parameters.Body = ($Body | ConvertTo-Json -Depth 16 -Compress)
    }
    try {
        $response = Invoke-WebRequest @parameters
        return [int]$response.StatusCode
    } catch {
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
        throw
    }
}

function Invoke-ControlStatus {
    param(
        [Microsoft.PowerShell.Commands.WebRequestSession]$Session,
        [string]$Csrf,
        [string]$Method,
        [string]$Path,
        [object]$Body
    )
    $headers = @{}
    if ($Method -notin @("GET", "HEAD")) { $headers["X-CSRF-Token"] = $Csrf }
    Invoke-Status $Method "$ControlUrl$Path" "" $Body $Session $headers
}

function Invoke-Gateway {
    param([string]$ApiKey, [string]$Model, [string]$InputText)
    $headers = @{ Authorization = "Bearer $ApiKey"; "X-Client-Request-Id" = "v2-$([Guid]::NewGuid().ToString('N'))" }
    try {
        $response = Invoke-WebRequest -Uri "$GatewayUrl/v1/responses" -Method POST -Headers $headers -ContentType "application/json" -Body (@{ model = $Model; input = $InputText } | ConvertTo-Json -Compress) -UseBasicParsing
        $responseBody = $null
        if (-not [string]::IsNullOrWhiteSpace([string]$response.Content)) {
            $responseBody = $response.Content | ConvertFrom-Json
        }
        return [pscustomobject]@{ Status = [int]$response.StatusCode; RequestID = [string]$response.Headers["X-Request-Id"]; Body = $responseBody }
    } catch {
        if (-not $_.Exception.Response) { throw }
        return [pscustomobject]@{ Status = [int]$_.Exception.Response.StatusCode; RequestID = [string]$_.Exception.Response.Headers["X-Request-Id"]; Body = $null }
    }
}

function Reset-Mock {
    Invoke-RestMethod -Uri "$MockUrl/__test/reset" -Method POST -Headers @{ "X-RelayDock-Test-Token" = $MockTestToken } -ContentType "application/json" -Body "{}" | Out-Null
}

function Set-MockScenario {
    param([hashtable]$Scenario)
    Invoke-RestMethod -Uri "$MockUrl/__test/scenario" -Method POST -Headers @{ "X-RelayDock-Test-Token" = $MockTestToken } -ContentType "application/json" -Body ($Scenario | ConvertTo-Json -Depth 8 -Compress) | Out-Null
}

function Get-MockRequests {
    @((Invoke-RestMethod -Uri "$MockUrl/__test/requests" -Headers @{ "X-RelayDock-Test-Token" = $MockTestToken }).data)
}

function Wait-For {
    param([scriptblock]$Probe, [int]$TimeoutSeconds = 20, [string]$Description = "condition")
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $value = & $Probe
        if ($null -ne $value -and $value -ne $false) { return $value }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Timed out waiting for $Description."
}

$run = [Guid]::NewGuid().ToString("N").Substring(0, 10)
$userPassword = "V2!$([Guid]::NewGuid().ToString('N'))"
$userEmail = "v2-$run@relayedock.local"
$otherUserEmail = "v2-other-$run@relayedock.local"
$routeAlias = "v2-chat-$run"
$csvFormulaAlias = "=v2-csv-$run"

$version = Invoke-RestMethod -Uri "$ControlUrl/api/version"
Assert-Equal ([string]$version.version) "2.0.0" "The V2 binary version endpoint is incorrect"

$adminSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$adminLogin = Invoke-RestMethod -Uri "$ControlUrl/api/admin/auth/login" -Method POST -WebSession $adminSession -ContentType "application/json" -Body (@{ email = $AdminEmail; password = $AdminPassword } | ConvertTo-Json -Compress)
$adminCsrf = [string]$adminLogin.csrf_token
Assert-True (-not [string]::IsNullOrWhiteSpace($adminCsrf)) "Admin login did not return a CSRF token"

$provider = Invoke-Control $adminSession $adminCsrf POST "/api/admin/providers" @{
    name = "V2 Mock $run"; slug = "v2-mock-$run"; provider_type = "openai"; base_url = $MockProviderBaseUrl; enabled = $true; config = @{}
}
$group = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credential-groups" @{
    provider_id = $provider.id; name = "V2 Pool $run"; description = "V2 deterministic functional test"
}

$credentialA = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credentials" @{
    provider_id = $provider.id; name = "V2 APAC $run"; secret = $MockProviderKey; group_id = $group.id
    validate = $true; priority = 100; weight = 100; max_concurrency = 8
}
$credentialB = Invoke-Control $adminSession $adminCsrf POST "/api/admin/credentials" @{
    provider_id = $provider.id; name = "V2 Maintenance $run"; secret = $MockProviderKey; group_id = $group.id
    validate = $true; priority = 10; weight = 100; max_concurrency = 8
}
$tagsA = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/credentials/$($credentialA.id)/tags" @{ tags = @("region:apac", "tier:production") }
$tagsB = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/credentials/$($credentialB.id)/tags" @{ tags = @("region:apac", "maintenance") }
Assert-True ($tagsA.tags -contains "region:apac") "Credential A tags were not persisted"
Assert-True ($tagsB.tags -contains "maintenance") "Credential B tags were not persisted"
$tagsARead = Invoke-Control $adminSession $adminCsrf GET "/api/admin/credentials/$($credentialA.id)/tags" $null
Assert-Equal $tagsARead.tags.Count 2 "Credential tag GET returned the wrong number of tags"
Assert-True ($tagsARead.tags -contains "region:apac") "Credential tag GET omitted a persisted tag"

$route = Invoke-Control $adminSession $adminCsrf POST "/api/admin/model-routes" @{
    alias = $routeAlias; provider_id = $provider.id; upstream_model = "mock-chat"; credential_group_id = $group.id
    routing_policy = "priority_weighted"; fallback_config = @{}
}
$csvFormulaRoute = Invoke-Control $adminSession $adminCsrf POST "/api/admin/model-routes" @{
    alias = $csvFormulaAlias; provider_id = $provider.id; upstream_model = "mock-chat"; credential_group_id = $group.id
    routing_policy = "priority_weighted"; fallback_config = @{}
}

$user = Invoke-Control $adminSession $adminCsrf POST "/api/admin/users" @{
    email = $userEmail; password = $userPassword; display_name = "V2 User $run"; role = "USER"
}
$otherUser = Invoke-Control $adminSession $adminCsrf POST "/api/admin/users" @{
    email = $otherUserEmail; password = $userPassword; display_name = "V2 Other $run"; role = "USER"
}

$orgA = Invoke-Control $adminSession $adminCsrf POST "/api/admin/organizations" @{
    name = "V2 Organization A $run"; slug = "v2-org-a-$run"; status = "ACTIVE"; metadata = @{ run = $run }
}
$orgB = Invoke-Control $adminSession $adminCsrf POST "/api/admin/organizations" @{
    name = "V2 Organization B $run"; slug = "v2-org-b-$run"; status = "ACTIVE"; metadata = @{ run = $run }
}
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/organizations/$($orgA.id)/members/$($user.id)" @{ role = "MEMBER"; status = "ACTIVE" } | Out-Null
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/organizations/$($orgB.id)/members/$($otherUser.id)" @{ role = "MEMBER"; status = "ACTIVE" } | Out-Null

$projectA = Invoke-Control $adminSession $adminCsrf POST "/api/admin/organizations/$($orgA.id)/projects" @{
    name = "V2 Project A $run"; slug = "v2-project-a-$run"; description = "Primary V2 functional test"; status = "ACTIVE"; metadata = @{}
}
$projectB = Invoke-Control $adminSession $adminCsrf POST "/api/admin/organizations/$($orgB.id)/projects" @{
    name = "V2 Project B $run"; slug = "v2-project-b-$run"; description = "Tenant isolation control"; status = "ACTIVE"; metadata = @{}
}
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/members/$($user.id)" @{ role = "DEVELOPER"; status = "ACTIVE" } | Out-Null
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectB.id)/members/$($otherUser.id)" @{ role = "DEVELOPER"; status = "ACTIVE" } | Out-Null

$grant = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/routes" @{
    model_route_id = $route.id; enabled = $true
    routing_config = @{ required_credential_tags = @("region:apac"); excluded_credential_tags = @("maintenance") }
}
Assert-Equal ([string]$grant.alias) $routeAlias "The project route grant did not preserve the alias"
$csvFormulaGrant = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/routes" @{
    model_route_id = $csvFormulaRoute.id; enabled = $true
    routing_config = @{ required_credential_tags = @("region:apac"); excluded_credential_tags = @("maintenance") }
}
Assert-Equal ([string]$csvFormulaGrant.alias) $csvFormulaAlias "The CSV-safety route grant did not preserve its formula-like alias"

$userSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$userLogin = Invoke-RestMethod -Uri "$ControlUrl/api/console/auth/login" -Method POST -WebSession $userSession -ContentType "application/json" -Body (@{ email = $userEmail; password = $userPassword } | ConvertTo-Json -Compress)
$userCsrf = [string]$userLogin.csrf_token
$otherSession = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$otherLogin = Invoke-RestMethod -Uri "$ControlUrl/api/console/auth/login" -Method POST -WebSession $otherSession -ContentType "application/json" -Body (@{ email = $otherUserEmail; password = $userPassword } | ConvertTo-Json -Compress)
$otherCsrf = [string]$otherLogin.csrf_token

$viewerMembership = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/members/$($user.id)" @{ role = "VIEWER"; status = "ACTIVE" }
Assert-Equal ([string]$viewerMembership.role) "VIEWER" "The RBAC fixture could not be moved to VIEWER"
$rbacViewerReadStatus = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/projects/$($projectA.id)/routes" $null
$rbacViewerRouteWriteStatus = Invoke-ControlStatus $userSession $userCsrf POST "/api/console/projects/$($projectA.id)/routes" @{
    model_route_id = $route.id; enabled = $false
}
$rbacViewerKeyCreateStatus = Invoke-ControlStatus $userSession $userCsrf POST "/api/console/api-keys" @{
    project_id = $projectA.id; name = "V2 forbidden viewer key $run"; environment = "test"; allowed_models = @($routeAlias)
}
Assert-Equal $rbacViewerReadStatus 200 "A project VIEWER could not read project routes"
Assert-Equal $rbacViewerRouteWriteStatus 404 "A project VIEWER was allowed to mutate project routes"
Assert-Equal $rbacViewerKeyCreateStatus 404 "A project VIEWER was allowed to create a project API key"

$developerMembership = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/members/$($user.id)" @{ role = "DEVELOPER"; status = "ACTIVE" }
Assert-Equal ([string]$developerMembership.role) "DEVELOPER" "The RBAC fixture could not be promoted to DEVELOPER"
$rbacDeveloperKeyResponse = Invoke-Control $userSession $userCsrf POST "/api/console/api-keys" @{
    project_id = $projectA.id; name = "V2 developer-owned key $run"; environment = "test"; allowed_models = @($routeAlias)
}
$rbacDeveloperKey = [string]$rbacDeveloperKeyResponse.key
$rbacDeveloperKeyID = [string]$rbacDeveloperKeyResponse.api_key.id
Assert-True (-not [string]::IsNullOrWhiteSpace($rbacDeveloperKey) -and -not [string]::IsNullOrWhiteSpace($rbacDeveloperKeyID)) "A project DEVELOPER could not create their own API key"
$rbacDeveloperKeyBeforeDeleteStatus = Invoke-Status GET "$GatewayUrl/v1/models" $rbacDeveloperKey $null $null
Assert-Equal $rbacDeveloperKeyBeforeDeleteStatus 200 "The project DEVELOPER's newly created API key was not usable"
$rbacDeveloperKeyDeleteStatus = Invoke-ControlStatus $userSession $userCsrf DELETE "/api/console/api-keys/$rbacDeveloperKeyID" $null
Assert-Equal $rbacDeveloperKeyDeleteStatus 204 "A project DEVELOPER could not delete their own API key"
$rbacDeveloperDeletedKeyStatus = Invoke-Status GET "$GatewayUrl/v1/models" $rbacDeveloperKey $null $null
Assert-Equal $rbacDeveloperDeletedKeyStatus 401 "A project DEVELOPER's deleted API key remained usable"

$adminMembership = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/members/$($user.id)" @{ role = "ADMIN"; status = "ACTIVE" }
Assert-Equal ([string]$adminMembership.role) "ADMIN" "The RBAC fixture could not be promoted to project ADMIN"
$rbacAdminRouteMutationStatus = Invoke-ControlStatus $userSession $userCsrf PUT "/api/console/projects/$($projectA.id)/routes/$($route.id)" @{
    enabled = $false
    routing_config = @{ required_credential_tags = @("region:apac"); excluded_credential_tags = @("maintenance") }
}
Assert-Equal $rbacAdminRouteMutationStatus 200 "A project ADMIN could not mutate a project route"
$rbacAdminMutatedRoutes = @((Invoke-Control $userSession $userCsrf GET "/api/console/projects/$($projectA.id)/routes" $null).data)
$rbacAdminMutatedRoute = @($rbacAdminMutatedRoutes | Where-Object alias -eq $routeAlias | Select-Object -First 1)
Assert-Equal $rbacAdminMutatedRoute.Count 1 "The project ADMIN route mutation removed the route instead of updating it"
Assert-True (-not [bool]$rbacAdminMutatedRoute[0].enabled) "The project ADMIN route mutation was not persisted"
$rbacAdminRouteRestoreStatus = Invoke-ControlStatus $userSession $userCsrf PUT "/api/console/projects/$($projectA.id)/routes/$($route.id)" @{
    enabled = $true
    routing_config = @{ required_credential_tags = @("region:apac"); excluded_credential_tags = @("maintenance") }
}
Assert-Equal $rbacAdminRouteRestoreStatus 200 "The project ADMIN could not restore the RBAC probe route"
$rbacCleanupRoutes = @((Invoke-Control $userSession $userCsrf GET "/api/console/projects/$($projectA.id)/routes" $null).data)
$rbacCleanupRoute = @($rbacCleanupRoutes | Where-Object alias -eq $routeAlias | Select-Object -First 1)
Assert-Equal $rbacCleanupRoute.Count 1 "The RBAC probe cleanup could not find the restored project route"
Assert-True ([bool]$rbacCleanupRoute[0].enabled) "The RBAC probe cleanup left the project route disabled"
$rbacCleanupMembership = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/members/$($user.id)" @{ role = "DEVELOPER"; status = "ACTIVE" }
Assert-Equal ([string]$rbacCleanupMembership.role) "DEVELOPER" "The RBAC probe did not restore the project member's DEVELOPER role"

$ownProjectRoutesStatus = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/projects/$($projectA.id)/routes" $null
Assert-Equal $ownProjectRoutesStatus 200 "A Project A developer could not read their own route grants"
$userVisibleProjects = @((Invoke-Control $userSession $userCsrf GET "/api/console/projects?limit=200" $null).data)
$otherVisibleProjects = @((Invoke-Control $otherSession $otherCsrf GET "/api/console/projects?limit=200" $null).data)
$userVisibleProjectIDs = @($userVisibleProjects | ForEach-Object { [string]$_.id })
$otherVisibleProjectIDs = @($otherVisibleProjects | ForEach-Object { [string]$_.id })
Assert-True ($userVisibleProjectIDs -contains ([string]$projectA.id)) "The Console project list omitted Project A for its member"
Assert-True (-not ($userVisibleProjectIDs -contains ([string]$projectB.id))) "The Console project list exposed Project B to a Project A member"
Assert-True ($otherVisibleProjectIDs -contains ([string]$projectB.id)) "The Console project list omitted Project B for its member"
Assert-True (-not ($otherVisibleProjectIDs -contains ([string]$projectA.id))) "The Console project list exposed Project A to a Project B member"
$crossProjectChecks = [ordered]@{
    project = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/projects/$($projectB.id)" $null
    routes = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/projects/$($projectB.id)/routes" $null
    budgets = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/projects/$($projectB.id)/budgets" $null
    webhooks = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/projects/$($projectB.id)/webhooks" $null
    webhook_deliveries = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/projects/$($projectB.id)/webhook-deliveries" $null
    usage_export = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/projects/$($projectB.id)/usage/export" $null
    models = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/models?project_id=$($projectB.id)" $null
    overview = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/overview?project_id=$($projectB.id)" $null
    usage = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/usage?project_id=$($projectB.id)" $null
    logs = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/logs?project_id=$($projectB.id)" $null
    api_keys = Invoke-ControlStatus $userSession $userCsrf GET "/api/console/api-keys?project_id=$($projectB.id)" $null
}
foreach ($check in $crossProjectChecks.GetEnumerator()) {
    Assert-Equal ([int]$check.Value) 404 "A Project A member could access Project B through the Console $($check.Key) endpoint"
}
$crossProjectStatus = [int]$crossProjectChecks.routes
$otherUserCrossProjectStatus = Invoke-ControlStatus $otherSession $otherCsrf GET "/api/console/models?project_id=$($projectA.id)" $null
Assert-Equal $otherUserCrossProjectStatus 404 "A Project B member could enumerate Project A models"
$rbacRouteWriteStatus = Invoke-ControlStatus $userSession $userCsrf POST "/api/console/projects/$($projectA.id)/routes" @{
    model_route_id = $route.id; enabled = $false
}
Assert-Equal $rbacRouteWriteStatus 404 "A project DEVELOPER was allowed to perform an ADMIN route mutation"

$successWebhook = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhooks" @{
    name = "V2 receiver $run"; url = "http://mock-openai:8090/webhooks/receiver"; signing_secret = $MockWebhookSecret
    event_types = @("webhook.test", "budget.warning", "budget.exceeded", "api_key.rotated"); enabled = $true
}
$failureWebhook = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhooks" @{
    name = "V2 failing receiver $run"; url = "http://mock-openai:8090/webhooks/fail"; signing_secret = $MockWebhookSecret
    event_types = @("webhook.test"); enabled = $true
}
$successWebhookID = [string]$successWebhook.webhook.id
$failureWebhookID = [string]$failureWebhook.webhook.id
$disabledWebhook = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/webhooks/$successWebhookID" @{
    name = "V2 receiver updated $run"; enabled = $false
}
Assert-Equal ([string]$disabledWebhook.name) "V2 receiver updated $run" "Webhook update did not persist its new name"
Assert-True (-not [bool]$disabledWebhook.enabled) "Webhook update did not disable the endpoint"
Assert-True ($disabledWebhook.event_types -contains "webhook.test") "A partial webhook update discarded existing event subscriptions"
$disabledWebhookTestStatus = Invoke-ControlStatus $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhooks/$successWebhookID/test" @{}
Assert-Equal $disabledWebhookTestStatus 404 "A disabled webhook endpoint still accepted a test delivery"
$reenabledWebhook = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/webhooks/$successWebhookID" @{
    enabled = $true
}
Assert-True ([bool]$reenabledWebhook.enabled) "Webhook update did not re-enable the endpoint"

$warningPolicy = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/budgets" @{
    name = "V2 warning $run"; period = "MONTHLY"; token_limit = 10; alert_threshold = 0.5; enforce_hard_limit = $false; status = "ACTIVE"
}

$keyResponse = Invoke-Control $adminSession $adminCsrf POST "/api/admin/api-keys" @{
    user_id = $user.id; organization_id = $orgA.id; project_id = $projectA.id; name = "V2 key $run"; environment = "test"
    rate_limit_rpm = 120; rate_limit_tpm = 200000; allowed_models = @($routeAlias, $csvFormulaAlias, "not-granted-$run")
}
$oldKey = [string]$keyResponse.key
$keyID = [string]$keyResponse.api_key.id
Assert-True ($oldKey.StartsWith("rdk_test_")) "Project API key creation did not return a one-time test secret"
$unknownKeySuffix = if ($oldKey.EndsWith("A")) { "E" } else { "A" }
$unknownKey = $oldKey.Substring(0, $oldKey.Length - 1) + $unknownKeySuffix
$unknownKeyStatus = Invoke-Status GET "$GatewayUrl/v1/models" $unknownKey $null $null
Assert-Equal $unknownKeyStatus 401 "A syntactically valid but unknown API key was accepted"

$expiredKeyResponse = Invoke-Control $adminSession $adminCsrf POST "/api/admin/api-keys" @{
    user_id = $user.id; organization_id = $orgA.id; project_id = $projectA.id; name = "V2 expired key $run"; environment = "test"
    expires_at = [DateTime]::UtcNow.AddMinutes(-5).ToString("yyyy-MM-ddTHH:mm:ssZ")
    rate_limit_rpm = 120; rate_limit_tpm = 200000; allowed_models = @($routeAlias)
}
$expiredKey = [string]$expiredKeyResponse.key
$expiredKeyStatus = Invoke-Status GET "$GatewayUrl/v1/models" $expiredKey $null $null
Assert-Equal $expiredKeyStatus 401 "An expired project API key was accepted by the Gateway"

$crossUserRotateStatus = Invoke-ControlStatus $otherSession $otherCsrf POST "/api/console/api-keys/$keyID/rotate" @{ grace_seconds = 60 }
$crossUserFinalizeStatus = Invoke-ControlStatus $otherSession $otherCsrf POST "/api/console/api-keys/$keyID/finalize" @{ version = 1 }
Assert-Equal $crossUserRotateStatus 404 "A different user could rotate another user's API key"
Assert-Equal $crossUserFinalizeStatus 404 "A different user could finalize another user's API-key rotation"

# Create a real Project B ledger row so the later CSV isolation assertion has
# a non-empty cross-project control dataset. Without this row, a broken export
# that returned every request could still appear isolated.
Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectB.id)/routes" @{
    model_route_id = $route.id; enabled = $true
    routing_config = @{ required_credential_tags = @("region:apac"); excluded_credential_tags = @("maintenance") }
} | Out-Null
$otherKeyResponse = Invoke-Control $adminSession $adminCsrf POST "/api/admin/api-keys" @{
    user_id = $otherUser.id; organization_id = $orgB.id; project_id = $projectB.id; name = "V2 isolation key $run"; environment = "test"
    rate_limit_rpm = 120; rate_limit_tpm = 200000; allowed_models = @($routeAlias)
}
$otherKey = [string]$otherKeyResponse.key
$otherKeyID = [string]$otherKeyResponse.api_key.id
Reset-Mock
$projectBControl = Invoke-Gateway $otherKey $routeAlias "Project B CSV isolation control"
Assert-Equal $projectBControl.Status 200 "The Project B CSV isolation control request failed"
$projectBControlLog = Wait-For -Description "the Project B CSV isolation control ledger row" -Probe {
    $logs = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/request-logs?limit=200" $null).data
    $logs | Where-Object request_id -eq $projectBControl.RequestID | Select-Object -First 1
}
Assert-Equal ([string]$projectBControlLog.project_id) ([string]$projectB.id) "The CSV isolation control row was not scoped to Project B"

$projectAConsoleModels = @((Invoke-Control $userSession $userCsrf GET "/api/console/models?project_id=$($projectA.id)" $null).data)
$projectBConsoleModels = @((Invoke-Control $otherSession $otherCsrf GET "/api/console/models?project_id=$($projectB.id)" $null).data)
$projectAConsoleModelIDs = @($projectAConsoleModels | ForEach-Object { [string]$_.id })
$projectBConsoleModelIDs = @($projectBConsoleModels | ForEach-Object { [string]$_.id })
Assert-True ($projectAConsoleModelIDs -contains $csvFormulaAlias) "Project A Console models omitted its project-only CSV-safety route"
Assert-True (-not ($projectBConsoleModelIDs -contains $csvFormulaAlias)) "Project B Console models leaked Project A's project-only route"
Assert-True ($projectBConsoleModelIDs -contains $routeAlias) "Project B Console models omitted its own granted route"
$projectAConsoleKeys = @((Invoke-Control $userSession $userCsrf GET "/api/console/api-keys?project_id=$($projectA.id)&limit=200" $null).data)
$projectBConsoleKeys = @((Invoke-Control $otherSession $otherCsrf GET "/api/console/api-keys?project_id=$($projectB.id)&limit=200" $null).data)
$projectAConsoleKeyIDs = @($projectAConsoleKeys | ForEach-Object { [string]$_.id })
$projectBConsoleKeyIDs = @($projectBConsoleKeys | ForEach-Object { [string]$_.id })
Assert-True ($projectAConsoleKeyIDs -contains $keyID) "Project A Console API-key list omitted its own key"
Assert-True (-not ($projectAConsoleKeyIDs -contains $otherKeyID)) "Project A Console API-key list leaked a Project B key"
Assert-True ($projectBConsoleKeyIDs -contains $otherKeyID) "Project B Console API-key list omitted its own key"
Assert-True (-not ($projectBConsoleKeyIDs -contains $keyID)) "Project B Console API-key list leaked a Project A key"

$modelCatalog = Invoke-RestMethod -Uri "$GatewayUrl/v1/models" -Headers @{ Authorization = "Bearer $oldKey" }
$visibleModelIDs = @($modelCatalog.data | ForEach-Object { [string]$_.id })
Assert-True ($visibleModelIDs -contains $routeAlias) "The granted project alias was missing from the Gateway model catalog"
Assert-True ($visibleModelIDs -contains $csvFormulaAlias) "The granted CSV-safety alias was missing from the Gateway model catalog"
Assert-True (-not ($visibleModelIDs -contains "not-granted-$run")) "The Gateway model catalog exposed an ungranted project alias"

Reset-Mock
$ungranted = Invoke-Gateway $oldKey "not-granted-$run" "This alias is not granted"
$ungrantedUpstreamCount = @(Get-MockRequests).Count
Assert-Equal $ungranted.Status 404 "An ungranted project alias did not return model_not_found"
Assert-Equal $ungrantedUpstreamCount 0 "An ungranted model reached the upstream provider"

Reset-Mock
$validInput = "V2 input to output functional request"
$valid = Invoke-Gateway $oldKey $routeAlias $validInput
$validUpstream = @(Get-MockRequests)
Assert-Equal $valid.Status 200 "The granted V2 route did not proxy successfully"
Assert-Equal $validUpstream.Count 1 "The valid request did not produce exactly one upstream call"
Assert-Equal ([string]$validUpstream[0].model) "mock-chat" "RelayDock did not rewrite the project alias to its upstream model"
Assert-Equal ([string]$validUpstream[0].body.input) $validInput "RelayDock did not forward the input payload intact"
Assert-Equal ([string]$valid.Body.status) "completed" "The proxied Responses output was not completed"
Assert-Equal ([string]$valid.Body.output[0].content[0].text) "RelayDock mock response" "The deterministic upstream output was not returned to the client"

$loggedRequest = Wait-For -Description "the scoped request log" -Probe {
    $logs = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/request-logs?limit=200" $null).data
    $logs | Where-Object request_id -eq $valid.RequestID | Select-Object -First 1
}
Assert-Equal ([string]$loggedRequest.credential_id) ([string]$credentialA.id) "Tag routing did not select the allowed APAC credential"
Assert-True ($loggedRequest.scheduler_reason.required_credential_tags -contains "region:apac") "Scheduler reason omitted the required tag constraint"
Assert-True ($loggedRequest.scheduler_reason.excluded_credential_tags -contains "maintenance") "Scheduler reason omitted the excluded tag constraint"

$warningEvent = Wait-For -Description "the budget warning event" -Probe {
    $events = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/budget-events?limit=100" $null).data
    $events | Where-Object { $_.policy_id -eq $warningPolicy.id -and $_.event_type -eq "THRESHOLD" } | Select-Object -First 1
}
$warningWebhookReceipt = Wait-For -Description "the budget warning webhook" -Probe {
    $received = @((Invoke-RestMethod -Uri "$MockUrl/webhooks/received").data)
    $received | Where-Object { $_.event -eq "budget.warning" -and $_.payload.id -eq $warningEvent.id } | Select-Object -First 1
}
Assert-True ([bool]$warningWebhookReceipt.signature_valid) "The budget warning webhook signature was invalid"

$routeDisableWriteStatus = Invoke-ControlStatus $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/routes/$($route.id)" @{
    enabled = $false
    routing_config = @{ required_credential_tags = @("region:apac"); excluded_credential_tags = @("maintenance") }
}
Assert-Equal $routeDisableWriteStatus 200 "The project route disable request did not return the persisted disabled route"
$disabledRoutes = @((Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/routes" $null).data)
$disabledRouteState = @($disabledRoutes | Where-Object alias -eq $routeAlias | Select-Object -First 1)
Assert-Equal $disabledRouteState.Count 1 "The disabled project route disappeared from the administrative route list"
Assert-True (-not [bool]$disabledRouteState[0].enabled) "The project route disable state was not persisted"
$disabledConsoleModelIDs = @((Invoke-Control $userSession $userCsrf GET "/api/console/models?project_id=$($projectA.id)" $null).data | ForEach-Object { [string]$_.id })
Assert-True (-not ($disabledConsoleModelIDs -contains $routeAlias)) "A disabled project route remained visible in the Console model catalog"
$disabledCatalog = Invoke-RestMethod -Uri "$GatewayUrl/v1/models" -Headers @{ Authorization = "Bearer $oldKey" }
$disabledCatalogIDs = @($disabledCatalog.data | ForEach-Object { [string]$_.id })
Assert-True (-not ($disabledCatalogIDs -contains $routeAlias)) "A disabled project route remained visible in the Gateway model catalog"
Reset-Mock
$disabledRouteRequest = Invoke-Gateway $oldKey $routeAlias "Disabled routes must fail before upstream"
$disabledRouteUpstreamCount = @(Get-MockRequests).Count
Assert-Equal $disabledRouteRequest.Status 404 "A disabled project route did not return model_not_found"
Assert-Equal $disabledRouteUpstreamCount 0 "A disabled project route reached the upstream provider"
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/routes/$($route.id)" @{
    enabled = $true
    routing_config = @{ required_credential_tags = @("region:apac"); excluded_credential_tags = @("maintenance") }
} | Out-Null

$routeDeleteStatus = Invoke-ControlStatus $adminSession $adminCsrf DELETE "/api/admin/projects/$($projectA.id)/routes/$($route.id)" $null
Assert-Equal $routeDeleteStatus 204 "Deleting a project route did not return No Content"
$routesAfterDelete = @((Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/routes" $null).data)
$deletedRouteStateCount = @($routesAfterDelete | Where-Object alias -eq $routeAlias).Count
Assert-Equal $deletedRouteStateCount 0 "A deleted project route remained in the administrative route list"
$deletedConsoleModelIDs = @((Invoke-Control $userSession $userCsrf GET "/api/console/models?project_id=$($projectA.id)" $null).data | ForEach-Object { [string]$_.id })
Assert-True (-not ($deletedConsoleModelIDs -contains $routeAlias)) "A deleted project route remained visible in the Console model catalog"
$deletedCatalog = Invoke-RestMethod -Uri "$GatewayUrl/v1/models" -Headers @{ Authorization = "Bearer $oldKey" }
$deletedCatalogIDs = @($deletedCatalog.data | ForEach-Object { [string]$_.id })
Assert-True (-not ($deletedCatalogIDs -contains $routeAlias)) "A deleted project route remained visible in the Gateway model catalog"
Reset-Mock
$deletedRouteRequest = Invoke-Gateway $oldKey $routeAlias "Deleted routes must fail before upstream"
$deletedRouteUpstreamCount = @(Get-MockRequests).Count
Assert-Equal $deletedRouteRequest.Status 404 "A deleted project route did not return model_not_found"
Assert-Equal $deletedRouteUpstreamCount 0 "A deleted project route reached the upstream provider"
$restoredGrant = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/routes" @{
    model_route_id = $route.id; enabled = $true
    routing_config = @{ required_credential_tags = @("region:apac"); excluded_credential_tags = @("maintenance") }
}
Assert-Equal ([string]$restoredGrant.alias) $routeAlias "The project route could not be restored after deletion"
$restoredCatalog = Invoke-RestMethod -Uri "$GatewayUrl/v1/models" -Headers @{ Authorization = "Bearer $oldKey" }
Assert-True (@($restoredCatalog.data | ForEach-Object { [string]$_.id }) -contains $routeAlias) "The restored route did not return to the Gateway model catalog"
$restoredConsoleModelIDs = @((Invoke-Control $userSession $userCsrf GET "/api/console/models?project_id=$($projectA.id)" $null).data | ForEach-Object { [string]$_.id })
Assert-True ($restoredConsoleModelIDs -contains $routeAlias) "The restored route did not return to the Console model catalog"

Reset-Mock
$csvFormulaRequest = Invoke-Gateway $oldKey $csvFormulaAlias "Create a real formula-like CSV field"
$csvFormulaUpstream = @(Get-MockRequests)
Assert-Equal $csvFormulaRequest.Status 200 "The CSV-safety control request failed"
Assert-Equal $csvFormulaUpstream.Count 1 "The CSV-safety control request did not reach upstream exactly once"
Assert-Equal ([string]$csvFormulaUpstream[0].model) "mock-chat" "The CSV-safety route did not resolve to the expected upstream model"
$consoleProjectALogs = @((Invoke-Control $userSession $userCsrf GET "/api/console/logs?project_id=$($projectA.id)&limit=200" $null).data)
$consoleProjectARequestIDs = @($consoleProjectALogs | ForEach-Object { [string]$_.request_id })
Assert-True ($consoleProjectARequestIDs -contains $valid.RequestID) "Project A Console logs omitted its own successful request"
Assert-True (-not ($consoleProjectARequestIDs -contains $projectBControl.RequestID)) "Project A Console logs leaked the Project B control request"

Invoke-Control $adminSession $adminCsrf PUT "/api/admin/credentials/$($credentialA.id)/tags" @{ tags = @("region:apac", "maintenance") } | Out-Null
Reset-Mock
$tagRejected = Invoke-Gateway $oldKey $routeAlias "No credential may satisfy contradictory tags"
$tagRejectedUpstreamCount = @(Get-MockRequests).Count
Assert-Equal $tagRejected.Status 503 "Excluded credential tags did not remove all ineligible credentials"
Assert-Equal $tagRejectedUpstreamCount 0 "A tag-rejected request reached the upstream provider"
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/credentials/$($credentialA.id)/tags" @{ tags = @("region:apac", "tier:production") } | Out-Null

$blockPolicy = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/budgets" @{
    name = "V2 hard block $run"; period = "MONTHLY"; token_limit = 1; alert_threshold = 0.5; enforce_hard_limit = $true; status = "ACTIVE"
}
Reset-Mock
$budgetBlocked = Invoke-Gateway $oldKey $routeAlias "This request must be blocked before upstream"
$budgetBlockedUpstreamCount = @(Get-MockRequests).Count
Assert-Equal $budgetBlocked.Status 403 "The hard project budget did not reject the request"
Assert-Equal $budgetBlockedUpstreamCount 0 "A budget-blocked request reached the upstream provider"
$budgetRejectEvent = Wait-For -Description "the hard-budget rejection event" -Probe {
    $events = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/budget-events?limit=100" $null).data
    $events | Where-Object { $_.policy_id -eq $blockPolicy.id -and $_.event_type -eq "REJECT" } | Select-Object -First 1
}
$exceededWebhookReceipt = Wait-For -Description "the budget exceeded webhook" -Probe {
    $received = @((Invoke-RestMethod -Uri "$MockUrl/webhooks/received").data)
    $received | Where-Object { $_.event -eq "budget.exceeded" -and $_.payload.id -eq $budgetRejectEvent.id } | Select-Object -First 1
}
Assert-True ([bool]$exceededWebhookReceipt.signature_valid) "The budget exceeded webhook signature was invalid"

$rotation = Invoke-Control $adminSession $adminCsrf POST "/api/admin/api-keys/$keyID/rotate" @{ grace_seconds = 60 }
$newKey = [string]$rotation.key
$oldDuringGrace = Invoke-Status GET "$GatewayUrl/v1/models" $oldKey $null $null
$newDuringGrace = Invoke-Status GET "$GatewayUrl/v1/models" $newKey $null $null
Assert-Equal $oldDuringGrace 200 "The old API-key version was not accepted during grace"
Assert-Equal $newDuringGrace 200 "The new API-key version was not accepted after rotation"
Invoke-Control $adminSession $adminCsrf POST "/api/admin/api-keys/$keyID/finalize" @{ version = 1 } | Out-Null
$oldAfterFinalize = Invoke-Status GET "$GatewayUrl/v1/models" $oldKey $null $null
$newAfterFinalize = Invoke-Status GET "$GatewayUrl/v1/models" $newKey $null $null
Assert-Equal $oldAfterFinalize 401 "Finalizing rotation did not revoke the old version"
Assert-Equal $newAfterFinalize 200 "Finalizing rotation revoked the active version"
$rotationWebhookReceipt = Wait-For -Description "the API-key rotation webhook" -Probe {
    $received = @((Invoke-RestMethod -Uri "$MockUrl/webhooks/received").data)
    $received | Where-Object { $_.event -eq "api_key.rotated" -and $_.payload.data.api_key_id -eq $keyID } | Select-Object -First 1
}
Assert-True ([bool]$rotationWebhookReceipt.signature_valid) "The API-key rotation webhook signature was invalid"

$naturalGraceKeyResponse = Invoke-Control $adminSession $adminCsrf POST "/api/admin/api-keys" @{
    user_id = $user.id; organization_id = $orgA.id; project_id = $projectA.id; name = "V2 natural grace key $run"; environment = "test"
    rate_limit_rpm = 120; rate_limit_tpm = 200000; allowed_models = @($routeAlias)
}
$naturalGraceOldKey = [string]$naturalGraceKeyResponse.key
$naturalGraceKeyID = [string]$naturalGraceKeyResponse.api_key.id
Assert-True (-not [string]::IsNullOrWhiteSpace($naturalGraceOldKey) -and -not [string]::IsNullOrWhiteSpace($naturalGraceKeyID)) "The natural-grace probe could not create its parent API key"
$naturalGraceRotation = Invoke-Control $adminSession $adminCsrf POST "/api/admin/api-keys/$naturalGraceKeyID/rotate" @{ grace_seconds = 30 }
$naturalGraceNewKey = [string]$naturalGraceRotation.key
Assert-True (-not [string]::IsNullOrWhiteSpace($naturalGraceNewKey)) "The natural-grace probe did not receive the rotated API-key secret"
$naturalGraceOldDuring = Invoke-Status GET "$GatewayUrl/v1/models" $naturalGraceOldKey $null $null
$naturalGraceNewDuring = Invoke-Status GET "$GatewayUrl/v1/models" $naturalGraceNewKey $null $null
Assert-Equal $naturalGraceOldDuring 200 "The old API-key version was not accepted during the minimum natural grace period"
Assert-Equal $naturalGraceNewDuring 200 "The new API-key version was not accepted during the minimum natural grace period"
Start-Sleep -Seconds 32
$naturalGraceOldAfterExpiry = Invoke-Status GET "$GatewayUrl/v1/models" $naturalGraceOldKey $null $null
$naturalGraceNewAfterExpiry = Invoke-Status GET "$GatewayUrl/v1/models" $naturalGraceNewKey $null $null
Assert-Equal $naturalGraceOldAfterExpiry 401 "The old API-key version remained usable after its natural grace expiry"
Assert-Equal $naturalGraceNewAfterExpiry 200 "Natural grace expiry invalidated the active API-key version"
$naturalGraceParentDeleteStatus = Invoke-ControlStatus $adminSession $adminCsrf DELETE "/api/admin/api-keys/$naturalGraceKeyID" $null
Assert-Equal $naturalGraceParentDeleteStatus 204 "Deleting the naturally rotated parent API key did not return No Content"
$naturalGraceNewAfterParentDelete = Invoke-Status GET "$GatewayUrl/v1/models" $naturalGraceNewKey $null $null
Assert-Equal $naturalGraceNewAfterParentDelete 401 "Deleting the naturally rotated parent API key did not invalidate its active version"

try { Invoke-WebRequest -Uri "$MockUrl/webhooks/received" -Method DELETE -UseBasicParsing | Out-Null } catch { throw }
$webhookTest = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhooks/$successWebhookID/test" @{}
$successDelivery = Wait-For -Description "successful signed webhook delivery" -Probe {
    $deliveries = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/webhook-deliveries?status=DELIVERED&limit=100" $null).data
    $deliveries | Where-Object id -eq $webhookTest.id | Select-Object -First 1
}
$receivedWebhook = Wait-For -Description "the mock webhook receipt" -Probe {
    $received = @((Invoke-RestMethod -Uri "$MockUrl/webhooks/received").data)
    $received | Where-Object delivery_id -eq $webhookTest.id | Select-Object -First 1
}
Assert-True ([bool]$receivedWebhook.signature_valid) "The webhook HMAC signature was invalid"
Assert-Equal ([string]$receivedWebhook.event) "webhook.test" "The webhook event header was incorrect"

$failedWebhookTest = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhooks/$failureWebhookID/test" @{}
$deadDelivery = Wait-For -TimeoutSeconds 20 -Description "the webhook dead-letter state" -Probe {
    $deliveries = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/webhook-deliveries?status=DEAD&limit=100" $null).data
    $deliveries | Where-Object id -eq $failedWebhookTest.id | Select-Object -First 1
}
Assert-Equal ([int]$deadDelivery.attempts) ([int]$deadDelivery.max_attempts) "The failed webhook did not exhaust its configured attempts"
$crossProjectRetryStatus = Invoke-ControlStatus $adminSession $adminCsrf POST "/api/admin/projects/$($projectB.id)/webhook-deliveries/$($deadDelivery.id)/retry" @{}
Assert-Equal $crossProjectRetryStatus 404 "A webhook delivery could be retried through a different project scope"
$deadDeliveryAfterCrossProjectRetry = @((Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/webhook-deliveries?status=DEAD&limit=100" $null).data | Where-Object id -eq $deadDelivery.id | Select-Object -First 1)
Assert-Equal $deadDeliveryAfterCrossProjectRetry.Count 1 "A rejected cross-project retry changed or hid the original dead-letter delivery"
$retriedDelivery = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhook-deliveries/$($deadDelivery.id)/retry" @{}
Assert-True ($retriedDelivery.status -in @("PENDING", "PROCESSING")) "Manual dead-letter retry did not requeue the delivery"

$webhookDeleteStatus = Invoke-ControlStatus $adminSession $adminCsrf DELETE "/api/admin/projects/$($projectA.id)/webhooks/$failureWebhookID" $null
Assert-Equal $webhookDeleteStatus 204 "Soft-deleting a webhook did not return No Content"
$deletedWebhookTestStatus = Invoke-ControlStatus $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhooks/$failureWebhookID/test" @{}
Assert-Equal $deletedWebhookTestStatus 404 "A soft-deleted webhook endpoint still accepted test deliveries"
$webhooksAfterDelete = @((Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/webhooks" $null).data)
$deletedWebhookState = @($webhooksAfterDelete | Where-Object id -eq $failureWebhookID | Select-Object -First 1)
Assert-Equal $deletedWebhookState.Count 1 "Soft-deleting a webhook removed its auditable configuration record"
Assert-True (-not [bool]$deletedWebhookState[0].enabled) "Soft-deleting a webhook did not leave it disabled"

$mockWebhookSecretLast4 = $MockWebhookSecret.Substring([Math]::Max(0, $MockWebhookSecret.Length - 4))
do {
    $replacementOldWebhookSecret = [Guid]::NewGuid().ToString("N")
} while ($replacementOldWebhookSecret.EndsWith($mockWebhookSecretLast4))
$replacementOldSecretLast4 = $replacementOldWebhookSecret.Substring($replacementOldWebhookSecret.Length - 4)
$replacementWebhookResponse = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhooks" @{
    name = "V2 replace-secret receiver $run"; url = "http://mock-openai:8090/webhooks/receiver"; signing_secret = $replacementOldWebhookSecret
    event_types = @("webhook.test"); enabled = $true
}
$replacementWebhookID = [string]$replacementWebhookResponse.webhook.id
Assert-Equal ([string]$replacementWebhookResponse.webhook.secret_last4) $replacementOldSecretLast4 "The replacement probe did not persist the original signing-secret last4"
$replacementWebhookUpdate = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/webhooks/$replacementWebhookID" @{
    signing_secret = $MockWebhookSecret
}
$replacementWebhookUpdateProperties = @($replacementWebhookUpdate.PSObject.Properties.Name)
$replacementWebhookUpdateJSON = [string]($replacementWebhookUpdate | ConvertTo-Json -Depth 16 -Compress)
$replacementWebhookUpdateEchoedSecret = ($replacementWebhookUpdateProperties -contains "signing_secret") -or
    ($replacementWebhookUpdateProperties -contains "secret") -or ($replacementWebhookUpdateProperties -contains "encrypted_secret") -or
    $replacementWebhookUpdateJSON.Contains('"encrypted_secret"') -or $replacementWebhookUpdateJSON.Contains($MockWebhookSecret) -or
    $replacementWebhookUpdateJSON.Contains($replacementOldWebhookSecret)
Assert-True (-not $replacementWebhookUpdateEchoedSecret) "The webhook signing-secret update response echoed secret material"
Assert-Equal ([string]$replacementWebhookUpdate.secret_last4) $mockWebhookSecretLast4 "The webhook signing-secret update returned the wrong last4"
$replacementWebhookLast4Changed = [string]$replacementWebhookUpdate.secret_last4 -cne $replacementOldSecretLast4
Assert-True $replacementWebhookLast4Changed "Replacing the webhook signing secret did not change its last4"
$replacementWebhookTest = Invoke-Control $adminSession $adminCsrf POST "/api/admin/projects/$($projectA.id)/webhooks/$replacementWebhookID/test" @{}
$replacementWebhookDelivery = Wait-For -Description "the replacement-secret webhook delivery" -Probe {
    $deliveries = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/webhook-deliveries?status=DELIVERED&limit=100" $null).data
    $deliveries | Where-Object id -eq $replacementWebhookTest.id | Select-Object -First 1
}
$replacementWebhookReceipt = Wait-For -Description "the replacement-secret mock webhook receipt" -Probe {
    $received = @((Invoke-RestMethod -Uri "$MockUrl/webhooks/received").data)
    $received | Where-Object delivery_id -eq $replacementWebhookTest.id | Select-Object -First 1
}
Assert-True ([bool]$replacementWebhookReceipt.signature_valid) "The webhook delivery did not use the replacement signing secret"
$replacementWebhookDeleteStatus = Invoke-ControlStatus $adminSession $adminCsrf DELETE "/api/admin/projects/$($projectA.id)/webhooks/$replacementWebhookID" $null
Assert-Equal $replacementWebhookDeleteStatus 204 "Deleting the replacement-secret webhook did not return No Content"

$fromDate = [DateTime]::UtcNow.AddDays(-1).ToString("yyyy-MM-dd")
$toDate = [DateTime]::UtcNow.ToString("yyyy-MM-dd")
$csvResponse = Invoke-WebRequest -Uri "$ControlUrl/api/admin/projects/$($projectA.id)/usage/export?from=$fromDate&to=$toDate" -WebSession $adminSession -UseBasicParsing
$csv = [string]$csvResponse.Content
Assert-Equal ([int]$csvResponse.StatusCode) 200 "Project CSV export failed"
$expectedCSVHeader = "request_id,organization_id,project_id,user_id,api_key_id,route_id,model,endpoint,status_code,input_tokens,cached_input_tokens,output_tokens,total_tokens,estimated_cost,latency_ms,created_at"
$actualCSVHeader = [string](@($csv -split "\r?\n")[0])
$csvContentType = [string]$csvResponse.Headers["Content-Type"]
$csvContentDisposition = [string]$csvResponse.Headers["Content-Disposition"]
Assert-Equal $actualCSVHeader $expectedCSVHeader "Project CSV export returned an unexpected schema header"
Assert-True ($csvContentType.StartsWith("text/csv")) "Project CSV export returned the wrong Content-Type"
Assert-True ($csvContentDisposition.Contains("relayedock-project-usage.csv")) "Project CSV export omitted its attachment filename"
$csvRows = @($csv | ConvertFrom-Csv)
$csvProjectIDs = @($csvRows | ForEach-Object { [string]$_.project_id } | Select-Object -Unique)
$csvProjectIsolated = ($csvProjectIDs -contains ([string]$projectA.id)) -and -not ($csvProjectIDs -contains ([string]$projectB.id))
Assert-True ($csvProjectIDs -contains ([string]$projectA.id)) "The date-only export range omitted the current Project A run"
Assert-True $csvProjectIsolated "Project CSV leaked Project B data"
$csvFormulaRow = @($csvRows | Where-Object request_id -eq $csvFormulaRequest.RequestID | Select-Object -First 1)
Assert-Equal $csvFormulaRow.Count 1 "Project CSV omitted the formula-injection control row"
Assert-Equal ([string]$csvFormulaRow[0].model) ("'" + $csvFormulaAlias) "Project CSV did not neutralize a formula-like model cell"
Assert-True (-not $csv.Contains(",$csvFormulaAlias,")) "Project CSV retained a raw formula-triggering model cell"
Assert-True (-not $csv.Contains($oldKey) -and -not $csv.Contains($newKey) -and -not $csv.Contains($expiredKey) -and
    -not $csv.Contains($rbacDeveloperKey) -and -not $csv.Contains($naturalGraceOldKey) -and -not $csv.Contains($naturalGraceNewKey) -and
    -not $csv.Contains($MockWebhookSecret) -and -not $csv.Contains($replacementOldWebhookSecret) -and -not $csv.Contains($MockProviderKey)) "Project CSV contained a secret"

$malformedCSVRangeStatus = Invoke-ControlStatus $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/usage/export?from=not-a-date&to=$toDate" $null
$futureFromDate = [DateTime]::UtcNow.AddDays(2).ToString("yyyy-MM-dd")
$reversedCSVRangeStatus = Invoke-ControlStatus $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/usage/export?from=$futureFromDate&to=$toDate" $null
$tooWideFromDate = [DateTime]::UtcNow.AddDays(-367).ToString("yyyy-MM-dd")
$tooWideCSVRangeStatus = Invoke-ControlStatus $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/usage/export?from=$tooWideFromDate&to=$toDate" $null
Assert-Equal $malformedCSVRangeStatus 400 "CSV export accepted an invalid from date"
Assert-Equal $reversedCSVRangeStatus 400 "CSV export accepted a range whose end was not later than its start"
Assert-Equal $tooWideCSVRangeStatus 400 "CSV export accepted a range larger than 366 days"

$membershipDisabled = Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/members/$($user.id)" @{ role = "DEVELOPER"; status = "DISABLED" }
$membershipDisabledKey = Invoke-Status GET "$GatewayUrl/v1/models" $newKey $null $null
Assert-Equal $membershipDisabledKey 401 "Disabling project membership did not invalidate its key"
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/members/$($user.id)" @{ role = "DEVELOPER"; status = "ACTIVE" } | Out-Null

Invoke-Control $adminSession $adminCsrf PUT "/api/admin/organizations/$($orgA.id)/members/$($user.id)" @{ role = "MEMBER"; status = "DISABLED" } | Out-Null
$organizationMembershipDisabledKey = Invoke-Status GET "$GatewayUrl/v1/models" $newKey $null $null
Assert-Equal $organizationMembershipDisabledKey 401 "Disabling organization membership did not invalidate its key"
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/organizations/$($orgA.id)/members/$($user.id)" @{ role = "MEMBER"; status = "ACTIVE" } | Out-Null

Invoke-Control $adminSession $adminCsrf PATCH "/api/admin/users/$($user.id)/status" @{ status = "DISABLED" } | Out-Null
$userDisabledKey = Invoke-Status GET "$GatewayUrl/v1/models" $newKey $null $null
Assert-Equal $userDisabledKey 401 "Disabling a user did not invalidate its key"
Invoke-Control $adminSession $adminCsrf PATCH "/api/admin/users/$($user.id)/status" @{ status = "ACTIVE" } | Out-Null

Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)" @{ status = "DISABLED" } | Out-Null
$projectDisabledKey = Invoke-Status GET "$GatewayUrl/v1/models" $newKey $null $null
Assert-Equal $projectDisabledKey 401 "Disabling a project did not invalidate its key"
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)" @{ status = "ACTIVE" } | Out-Null

Invoke-Control $adminSession $adminCsrf PUT "/api/admin/organizations/$($orgA.id)" @{ status = "DISABLED" } | Out-Null
$organizationDisabledKey = Invoke-Status GET "$GatewayUrl/v1/models" $newKey $null $null
Assert-Equal $organizationDisabledKey 401 "Disabling an organization did not invalidate its key"
Invoke-Control $adminSession $adminCsrf PUT "/api/admin/organizations/$($orgA.id)" @{ status = "ACTIVE" } | Out-Null

Invoke-Control $adminSession $adminCsrf PUT "/api/admin/projects/$($projectA.id)/budgets/$($blockPolicy.id)" @{
    name = $blockPolicy.name; period = $blockPolicy.period; token_limit = $blockPolicy.token_limit
    cost_limit = $blockPolicy.cost_limit; alert_threshold = $blockPolicy.alert_threshold
    enforce_hard_limit = $blockPolicy.enforce_hard_limit; status = "DISABLED"
} | Out-Null
Reset-Mock
Set-MockScenario @{ status = 401; once = $true }
$authFailure = Invoke-Gateway $newKey $routeAlias "Trigger a deterministic upstream credential authentication failure"
Assert-Equal $authFailure.Status 401 "The forced upstream authentication failure was not propagated"
$alert = Wait-For -Description "a credential alert" -Probe {
    $alerts = (Invoke-Control $adminSession $adminCsrf GET "/api/admin/alerts?limit=200" $null).data
    $alerts | Where-Object { $_.resource_id -eq $credentialA.id -and $_.status -eq "OPEN" } | Select-Object -First 1
}
$acknowledged = Invoke-Control $adminSession $adminCsrf POST "/api/admin/alerts/$($alert.id)/acknowledge" @{}
Assert-Equal ([string]$acknowledged.status) "ACKNOWLEDGED" "Alert acknowledgement did not update its status"
Assert-Equal ([string]$acknowledged.acknowledged_by) ([string]$adminLogin.user.id) "Alert acknowledgement did not persist the actor"
Invoke-Control $adminSession $adminCsrf PATCH "/api/admin/credentials/$($credentialA.id)/status" @{ status = "ACTIVE" } | Out-Null
Reset-Mock

$adminCredentialJSON = ((Invoke-Control $adminSession $adminCsrf GET "/api/admin/credentials?limit=200" $null) | ConvertTo-Json -Depth 16 -Compress)
$adminAPIKeysJSON = ((Invoke-Control $adminSession $adminCsrf GET "/api/admin/api-keys?limit=200" $null) | ConvertTo-Json -Depth 16 -Compress)
$adminLogsJSON = ((Invoke-Control $adminSession $adminCsrf GET "/api/admin/request-logs?limit=200" $null) | ConvertTo-Json -Depth 16 -Compress)
$adminWebhooksJSON = ((Invoke-Control $adminSession $adminCsrf GET "/api/admin/projects/$($projectA.id)/webhooks" $null) | ConvertTo-Json -Depth 16 -Compress)
$secretLeak = $adminCredentialJSON.Contains($MockProviderKey) -or $adminLogsJSON.Contains($MockProviderKey) -or
    $adminCredentialJSON.Contains($MockWebhookSecret) -or $adminWebhooksJSON.Contains($MockWebhookSecret) -or
    $adminWebhooksJSON.Contains($replacementOldWebhookSecret) -or $adminWebhooksJSON.Contains("encrypted_secret") -or
    $adminLogsJSON.Contains($oldKey) -or $adminLogsJSON.Contains($newKey) -or $adminLogsJSON.Contains($otherKey) -or
    $adminLogsJSON.Contains($expiredKey) -or $adminLogsJSON.Contains($rbacDeveloperKey) -or
    $adminLogsJSON.Contains($naturalGraceOldKey) -or $adminLogsJSON.Contains($naturalGraceNewKey) -or
    $adminAPIKeysJSON.Contains($oldKey) -or $adminAPIKeysJSON.Contains($newKey) -or
    $adminAPIKeysJSON.Contains($otherKey) -or $adminAPIKeysJSON.Contains($expiredKey) -or
    $adminAPIKeysJSON.Contains($rbacDeveloperKey) -or $adminAPIKeysJSON.Contains($naturalGraceOldKey) -or
    $adminAPIKeysJSON.Contains($naturalGraceNewKey)
Assert-True (-not $secretLeak) "A credential, signing secret, or API-key secret leaked through JSON APIs"

Invoke-Control $adminSession $adminCsrf DELETE "/api/admin/api-keys/$keyID" $null | Out-Null
$revokedParentKeyStatus = Invoke-Status GET "$GatewayUrl/v1/models" $newKey $null $null
Assert-Equal $revokedParentKeyStatus 401 "Revoking the parent API key did not invalidate its active version"

$summary = [ordered]@{
    version = [string]$version.version
    organization_create = "PASS"
    project_create = "PASS"
    membership_and_route_grant = "PASS"
    cross_project_status = $crossProjectStatus
    console_cross_project_endpoints = $crossProjectChecks
    console_reverse_cross_project_status = $otherUserCrossProjectStatus
    console_project_list_isolated = (-not ($userVisibleProjectIDs -contains ([string]$projectB.id)) -and -not ($otherVisibleProjectIDs -contains ([string]$projectA.id)))
    console_project_models_isolated = ($projectAConsoleModelIDs -contains $csvFormulaAlias -and -not ($projectBConsoleModelIDs -contains $csvFormulaAlias))
    console_project_keys_isolated = (-not ($projectAConsoleKeyIDs -contains $otherKeyID) -and -not ($projectBConsoleKeyIDs -contains $keyID))
    console_project_logs_isolated = (-not ($consoleProjectARequestIDs -contains $projectBControl.RequestID))
    developer_admin_mutation_status = $rbacRouteWriteStatus
    project_rbac_progression = [ordered]@{
        viewer_read_status = $rbacViewerReadStatus
        viewer_route_write_status = $rbacViewerRouteWriteStatus
        viewer_key_create_status = $rbacViewerKeyCreateStatus
        developer_key_created = (-not [string]::IsNullOrWhiteSpace($rbacDeveloperKeyID))
        developer_key_before_delete_status = $rbacDeveloperKeyBeforeDeleteStatus
        developer_key_delete_status = $rbacDeveloperKeyDeleteStatus
        developer_deleted_key_status = $rbacDeveloperDeletedKeyStatus
        admin_route_mutation_status = $rbacAdminRouteMutationStatus
        admin_route_restore_status = $rbacAdminRouteRestoreStatus
        cleanup_route_enabled = [bool]$rbacCleanupRoute[0].enabled
        cleanup_role = [string]$rbacCleanupMembership.role
    }
    model_catalog_project_isolated = ($visibleModelIDs -contains $routeAlias -and -not ($visibleModelIDs -contains "not-granted-$run"))
    unknown_api_key_status = $unknownKeyStatus
    expired_api_key_status = $expiredKeyStatus
    cross_user_rotate_finalize_status = @($crossUserRotateStatus, $crossUserFinalizeStatus)
    ungranted_model_status = $ungranted.Status
    ungranted_upstream_calls = $ungrantedUpstreamCount
    disabled_route_write_status = $routeDisableWriteStatus
    disabled_route_catalog_visible = ($disabledCatalogIDs -contains $routeAlias)
    disabled_route_gateway_status = $disabledRouteRequest.Status
    disabled_route_upstream_calls = $disabledRouteUpstreamCount
    deleted_route_catalog_visible = ($deletedCatalogIDs -contains $routeAlias)
    deleted_route_write_status = $routeDeleteStatus
    deleted_route_gateway_status = $deletedRouteRequest.Status
    deleted_route_upstream_calls = $deletedRouteUpstreamCount
    valid_proxy_status = $valid.Status
    valid_upstream_model = [string]$validUpstream[0].model
    valid_output_text = [string]$valid.Body.output[0].content[0].text
    selected_credential = ([string]$loggedRequest.credential_id -eq [string]$credentialA.id)
    budget_warning_event = [string]$warningEvent.event_type
    budget_warning_webhook = [string]$warningWebhookReceipt.event
    budget_block_status = $budgetBlocked.Status
    budget_block_upstream_calls = $budgetBlockedUpstreamCount
    budget_reject_event = [string]$budgetRejectEvent.event_type
    budget_exceeded_webhook = [string]$exceededWebhookReceipt.event
    tag_rejection_status = $tagRejected.Status
    tag_rejection_upstream_calls = $tagRejectedUpstreamCount
    rotation_old_new_during_grace = @($oldDuringGrace, $newDuringGrace)
    rotation_old_new_after_finalize = @($oldAfterFinalize, $newAfterFinalize)
    natural_grace_old_new_during = @($naturalGraceOldDuring, $naturalGraceNewDuring)
    natural_grace_old_new_after_expiry = @($naturalGraceOldAfterExpiry, $naturalGraceNewAfterExpiry)
    natural_grace_parent_delete_status = $naturalGraceParentDeleteStatus
    natural_grace_new_after_parent_delete = $naturalGraceNewAfterParentDelete
    revoked_parent_key_status = $revokedParentKeyStatus
    api_key_rotation_webhook = [string]$rotationWebhookReceipt.event
    webhook_signature_valid = [bool]$receivedWebhook.signature_valid
    webhook_success_status = [string]$successDelivery.status
    webhook_dead_status = [string]$deadDelivery.status
    webhook_dead_attempts = [int]$deadDelivery.attempts
    webhook_update_disabled_status = $disabledWebhookTestStatus
    webhook_cross_project_retry_status = $crossProjectRetryStatus
    webhook_manual_retry = [string]$retriedDelivery.status
    webhook_delete_status = $webhookDeleteStatus
    webhook_deleted_test_status = $deletedWebhookTestStatus
    webhook_secret_replacement_last4_changed = $replacementWebhookLast4Changed
    webhook_secret_replacement_response_echoed = $replacementWebhookUpdateEchoedSecret
    webhook_secret_replacement_signature_valid = [bool]$replacementWebhookReceipt.signature_valid
    webhook_secret_replacement_delivery_status = [string]$replacementWebhookDelivery.status
    webhook_secret_replacement_delete_status = $replacementWebhookDeleteStatus
    csv_project_isolated = $csvProjectIsolated
    csv_header_valid = ($actualCSVHeader -eq $expectedCSVHeader)
    csv_formula_cell = [string]$csvFormulaRow[0].model
    csv_invalid_range_statuses = @($malformedCSVRangeStatus, $reversedCSVRangeStatus, $tooWideCSVRangeStatus)
    membership_disabled_key_status = $membershipDisabledKey
    organization_membership_disabled_key_status = $organizationMembershipDisabledKey
    user_disabled_key_status = $userDisabledKey
    project_disabled_key_status = $projectDisabledKey
    organization_disabled_key_status = $organizationDisabledKey
    alert_acknowledged = [string]$acknowledged.status
    secret_leak_detected = $secretLeak
}
$summary | ConvertTo-Json -Depth 8
