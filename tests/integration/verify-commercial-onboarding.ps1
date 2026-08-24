[CmdletBinding()]
param(
    [switch]$ConfirmIsolatedTestDatabase,
    [ValidateRange(60, 240)]
    [int]$StartupTimeoutSeconds = 150,
    [string]$ExistingServerImage = ""
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

if (-not $ConfirmIsolatedTestDatabase) {
    throw "Pass -ConfirmIsolatedTestDatabase only for a disposable local Docker run."
}

$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$runID = [Guid]::NewGuid().ToString("N").Substring(0, 16)
$network = "modeldock-onboarding-$runID"
$postgres = "modeldock-onboarding-pg-$runID"
$redis = "modeldock-onboarding-redis-$runID"
$mock = "modeldock-onboarding-mock-$runID"
$server = "modeldock-onboarding-app-$runID"
$failoverServer = "modeldock-onboarding-app2-$runID"
$serverImage = if ($ExistingServerImage) { $ExistingServerImage } else { "modeldock/onboarding-integration:$runID" }
$ownsServerImage = -not [bool]$ExistingServerImage
$mockImage = "modeldock/onboarding-mock:$runID"
$sandboxSecret = "synthetic-sandbox-secret-32-bytes-$runID"
$mockAPIKey = "synthetic-mock-provider-key"
$mockTestToken = "synthetic-mock-control-token"
$containers = [Collections.Generic.List[string]]::new()

function Invoke-Docker {
    param([string[]]$Arguments, [string]$Operation)
    $savedPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $output = @(& docker @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedPreference
    }
    if ($exitCode -ne 0) {
        throw "$Operation failed; Docker diagnostic output was suppressed."
    }
    return @($output | ForEach-Object { [string]$_ })
}

function Wait-ContainerCommand {
    param([string]$Container, [string[]]$Command, [string]$Operation)
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $savedPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        do {
            & docker exec $Container @Command *> $null
            if ($LASTEXITCODE -eq 0) { return }
            Start-Sleep -Milliseconds 500
        } while ([DateTime]::UtcNow -lt $deadline)
    } finally {
        $ErrorActionPreference = $savedPreference
    }
    throw "$Operation did not become ready before the timeout."
}

function Wait-RedisCounterCleared {
    param([string]$Container, [string]$Key, [string]$Operation)
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    do {
        $value = [string]::Join("", @(Invoke-Docker -Arguments @("exec", $Container, "redis-cli", "--raw", "GET", $Key) -Operation $Operation)).Trim()
        $counter = 0L
        if ([string]::IsNullOrWhiteSpace($value) -or ([int64]::TryParse($value, [ref]$counter) -and $counter -le 0)) { return }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$Operation did not reach zero before the timeout."
}

function Wait-PostgresFinalStartup {
    param([string]$Container)
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $savedPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        do {
            $pidLine = [string]::Join("", @(& docker exec $Container head -n 1 /var/lib/postgresql/data/postmaster.pid 2>$null)).Trim()
            if ($LASTEXITCODE -eq 0 -and $pidLine -eq "1") {
                & docker exec $Container psql --no-psqlrc --username postgres --dbname postgres --command "SELECT 1" *> $null
                if ($LASTEXITCODE -eq 0) { return }
            }
            Start-Sleep -Milliseconds 500
        } while ([DateTime]::UtcNow -lt $deadline)
    } finally {
        $ErrorActionPreference = $savedPreference
    }
    throw "PostgreSQL final server did not become ready before the timeout."
}

function Get-PublishedPort {
    param([string]$Container, [int]$Port)
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $savedPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        do {
            $portOutput = @(& docker port $Container "$Port/tcp" 2>$null)
            if ($LASTEXITCODE -eq 0) {
                $line = $portOutput | Select-Object -First 1
                if ($line -match ':(\d+)$') { return [int]$Matches[1] }
            }
            Start-Sleep -Milliseconds 200
        } while ([DateTime]::UtcNow -lt $deadline)
    } finally {
        $ErrorActionPreference = $savedPreference
    }
    $bindings = [string]::Join(" ", @(& docker inspect --format '{{json .HostConfig.PortBindings}}' $Container 2>$null))
    $state = [string]::Join(" ", @(& docker inspect --format '{{.State.Status}}/exit={{.State.ExitCode}}' $Container 2>$null))
    $logs = [string]::Join("`n", @(& docker logs --tail 40 $Container 2>&1))
    $logs = $logs -replace 'postgres://[^\s"]+', 'postgres://[redacted]' -replace 'redis://[^\s"]+', 'redis://[redacted]' -replace 'rdk_(live|test)_[A-Za-z0-9_-]+', 'rdk_$1_[redacted]'
    throw "Reading container $Container published port $Port timed out; state=$state; bindings=$bindings; sanitized logs:`n$logs"
}

function Wait-HTTP {
    param([string]$URL)
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    do {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $URL -TimeoutSec 2
            if ($response.StatusCode -eq 200) { return }
        } catch { }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Application did not become ready before the timeout."
}

function Invoke-JSON {
    param(
        [string]$Method,
        [string]$URL,
        [object]$Body = $null,
        [Microsoft.PowerShell.Commands.WebRequestSession]$Session = $null,
        [hashtable]$Headers = @{}
    )
    $arguments = @{ UseBasicParsing = $true; Uri = $URL; Method = $Method; TimeoutSec = 20; Headers = $Headers }
    if ($null -ne $Session) { $arguments.WebSession = $Session }
    if ($null -ne $Body) {
        $arguments.ContentType = "application/json"
        $arguments.Body = ConvertTo-Json $Body -Depth 16 -Compress
    }
    $status = 0
    $content = ""
    try {
        $response = Invoke-WebRequest @arguments
        $status = [int]$response.StatusCode
        $content = [string]$response.Content
    } catch {
        $errorResponse = $_.Exception.Response
        if ($null -eq $errorResponse) { throw }
        $status = [int]$errorResponse.StatusCode
        if ($_.ErrorDetails -and -not [string]::IsNullOrWhiteSpace([string]$_.ErrorDetails.Message)) {
            $content = [string]$_.ErrorDetails.Message
        } else {
            $stream = $errorResponse.GetResponseStream()
            if ($null -ne $stream) {
                $reader = [IO.StreamReader]::new($stream)
                try { $content = $reader.ReadToEnd() } finally { $reader.Dispose(); $stream.Dispose() }
            }
        }
    }
    $json = $null
    if (-not [string]::IsNullOrWhiteSpace($content)) { $json = $content | ConvertFrom-Json }
    return [pscustomobject]@{ Status = $status; JSON = $json; Raw = $content }
}

function Invoke-RawJSON {
    param([string]$Method, [string]$URL, [string]$Body, [hashtable]$Headers)
    $arguments = @{ UseBasicParsing = $true; Uri = $URL; Method = $Method; TimeoutSec = 20; Headers = $Headers; ContentType = "application/json"; Body = $Body }
    try {
        $response = Invoke-WebRequest @arguments
        return [pscustomobject]@{ Status = [int]$response.StatusCode; JSON = $response.Content | ConvertFrom-Json; Raw = [string]$response.Content }
    } catch {
        $errorResponse = $_.Exception.Response
        if ($null -eq $errorResponse) { throw }
        $content = if ($_.ErrorDetails) { [string]$_.ErrorDetails.Message } else { "" }
        $json = if ($content) { $content | ConvertFrom-Json } else { $null }
        return [pscustomobject]@{ Status = [int]$errorResponse.StatusCode; JSON = $json; Raw = $content }
    }
}

function Assert-Status {
    param($Response, [int]$Expected, [string]$Operation)
    if ($Response.Status -ne $Expected) {
        $safe = [string]$Response.Raw -replace 'rdk_(live|test)_[A-Za-z0-9_-]+', 'rdk_$1_[redacted]'
        throw "$Operation returned HTTP $($Response.Status), expected $Expected. Response: $safe"
    }
}

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Get-CSRFHeader {
    param([Microsoft.PowerShell.Commands.WebRequestSession]$Session, [string]$BaseURL, [string]$CookieName)
    $cookie = $Session.Cookies.GetCookies([Uri]$BaseURL)[$CookieName]
    if ($null -eq $cookie -or [string]::IsNullOrWhiteSpace($cookie.Value)) { throw "Expected CSRF cookie is missing." }
    return @{ "X-CSRF-Token" = $cookie.Value }
}

function Invoke-SessionJSON {
    param(
        [string]$Method,
        [string]$URL,
        [Microsoft.PowerShell.Commands.WebRequestSession]$Session,
        [hashtable]$CSRF,
        [object]$Body = $null,
        [hashtable]$ExtraHeaders = @{}
    )
    $headers = @{}
    foreach ($item in $ExtraHeaders.GetEnumerator()) { $headers[$item.Key] = $item.Value }
    if ($Method -notin @("GET", "HEAD")) {
        foreach ($item in $CSRF.GetEnumerator()) { $headers[$item.Key] = $item.Value }
    }
    return Invoke-JSON -Method $Method -URL $URL -Body $Body -Session $Session -Headers $headers
}

function Get-MailFiles {
    $result = @(& docker exec $server sh -c 'find /tmp/mail -maxdepth 1 -type f -name "*.json" -print 2>/dev/null' 2>$null)
    if ($LASTEXITCODE -ne 0) { return @() }
    return @($result | ForEach-Object { [string]$_ })
}

function Wait-NewVerificationToken {
    param([string[]]$Before)
    $known = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($item in $Before) { [void]$known.Add($item) }
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        foreach ($file in (Get-MailFiles)) {
            if ($known.Contains($file)) { continue }
            $content = [string]::Join("`n", @(& docker exec $server sh -c "cat '$file'" 2>$null))
            if ($LASTEXITCODE -ne 0) { continue }
            $message = $content | ConvertFrom-Json
            if ([string]$message.text -match '[?&]token=([^&\s]+)') {
                return [Uri]::UnescapeDataString($Matches[1])
            }
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Expected captured verification email did not arrive."
}

function Invoke-Psql {
    param([string]$SQL)
    $output = Invoke-Docker -Arguments @("exec", $postgres, "psql", "--no-psqlrc", "--tuples-only", "--no-align", "--set", "ON_ERROR_STOP=1", "--username", "relaydock", "--dbname", "relaydock", "--command", $SQL) -Operation "Running an isolated verification query"
    return [string]::Join("`n", $output).Trim()
}

function Wait-PsqlValue {
    param([string]$SQL, [string]$Expected, [int]$TimeoutSeconds = 20, [string]$Operation = "database condition")
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $value = Invoke-Psql -SQL $SQL
        if ($value -eq $Expected) { return $value }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$Operation did not reach the expected value '$Expected' (last='$value')."
}

function Get-HMACSHA256Hex {
    param([string]$Secret, [string]$Value)
    $hmac = [Security.Cryptography.HMACSHA256]::new([Text.Encoding]::UTF8.GetBytes($Secret))
    try { $hash = $hmac.ComputeHash([Text.Encoding]::UTF8.GetBytes($Value)) } finally { $hmac.Dispose() }
    return [BitConverter]::ToString($hash).Replace("-", "").ToLowerInvariant()
}

try {
    if ($ownsServerImage) {
        [void](Invoke-Docker -Arguments @("build", "--quiet", "-f", (Join-Path $repoRoot "deploy/docker/Dockerfile.relaydock"), "-t", $serverImage, $repoRoot) -Operation "Building the onboarding server image")
    } else {
        [void](Invoke-Docker -Arguments @("image", "inspect", $serverImage) -Operation "Inspecting the prebuilt onboarding server image")
    }
    [void](Invoke-Docker -Arguments @("build", "--quiet", "-f", (Join-Path $repoRoot "deploy/mock-openai/Dockerfile"), "-t", $mockImage, $repoRoot) -Operation "Building the deterministic Provider image")
    [void](Invoke-Docker -Arguments @("network", "create", $network) -Operation "Creating the isolated network")

    [void](Invoke-Docker -Arguments @("run", "-d", "--name", $postgres, "--network", $network,
            "--tmpfs", "/var/lib/postgresql/data:rw,noexec,nosuid,size=256m",
            "-e", "POSTGRES_DB=postgres", "-e", "POSTGRES_USER=postgres", "-e", "POSTGRES_PASSWORD=synthetic-postgres-admin-password",
            "postgres:17-alpine") -Operation "Starting isolated PostgreSQL")
    $containers.Add($postgres)
    [void](Invoke-Docker -Arguments @("run", "-d", "--name", $redis, "--network", $network, "redis:7.4-alpine") -Operation "Starting isolated Redis")
    $containers.Add($redis)
    [void](Invoke-Docker -Arguments @("run", "-d", "--name", $mock, "--network", $network, "-p", "127.0.0.1::8090",
            "-e", "MOCK_OPENAI_API_KEY=$mockAPIKey", "-e", "MOCK_TEST_TOKEN=$mockTestToken", "-e", "MOCK_WEBHOOK_SECRET=synthetic-webhook-secret", $mockImage) -Operation "Starting deterministic Provider")
    $containers.Add($mock)

    Wait-PostgresFinalStartup -Container $postgres
    [void](Invoke-Docker -Arguments @("exec", $postgres, "psql", "--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--username", "postgres", "--dbname", "postgres", "--command", "CREATE ROLE relaydock LOGIN PASSWORD 'synthetic-db-password';") -Operation "Creating the isolated application role")
    [void](Invoke-Docker -Arguments @("exec", $postgres, "psql", "--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--username", "postgres", "--dbname", "postgres", "--command", "CREATE DATABASE relaydock OWNER relaydock;") -Operation "Creating the isolated application database")
    Wait-ContainerCommand -Container $postgres -Command @("psql", "--no-psqlrc", "--username", "relaydock", "--dbname", "relaydock", "--command", "SELECT 1") -Operation "PostgreSQL application database"
    Wait-ContainerCommand -Container $redis -Command @("redis-cli", "ping") -Operation "Redis"
    Wait-ContainerCommand -Container $mock -Command @("python3", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/healthz', timeout=2)") -Operation "mock Provider"

    $serverArguments = @(
        "run", "-d", "--name", $server, "--network", $network,
        "-p", "127.0.0.1::8080", "-p", "127.0.0.1::8081", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
        "-e", "DATABASE_URL=postgres://relaydock:synthetic-db-password@$postgres`:5432/relaydock?sslmode=disable",
        "-e", "REDIS_URL=redis://$redis`:6379/0",
        "-e", "RELAYDOCK_MASTER_KEY=0123456789abcdef0123456789abcdef",
        "-e", "RELAYDOCK_API_KEY_HMAC_SECRET=abcdef0123456789abcdef0123456789",
        "-e", "RELAYDOCK_JWT_SECRET=89abcdef0123456789abcdef01234567",
        "-e", "RELAYDOCK_ADMIN_EMAIL=admin@example.invalid",
        "-e", "RELAYDOCK_ADMIN_PASSWORD=synthetic-admin-password-2026",
        "-e", "RELAYDOCK_REGISTRATION_MODE=PUBLIC",
        "-e", "RELAYDOCK_MAIL_PROVIDER=local",
        "-e", "RELAYDOCK_MAIL_CAPTURE_DIR=/tmp/mail",
        "-e", "RELAYDOCK_PUBLIC_CONSOLE_URL=http://console.example.invalid",
        "-e", "RELAYDOCK_PUBLIC_SUPPORT_EMAIL=helpdesk@example.invalid",
        "-e", "RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL=sales@example.invalid",
        "-e", "RELAYDOCK_PROVIDER_ALLOWED_HOSTS=$mock",
        "-e", "RELAYDOCK_PROVIDER_ALLOW_HTTP=true",
        "-e", "RELAYDOCK_PROVIDER_ALLOW_PRIVATE_NETWORK=true",
        "-e", "RELAYDOCK_PROVIDER_TIMEOUT=2s",
        "-e", "RELAYDOCK_FUNDING_RECOVERY_INTERVAL=250ms",
        "-e", "RELAYDOCK_FUNDING_STALE_AFTER=30s",
        "-e", "RELAYDOCK_PAYMENT_ORDER_TTL=3s",
        "-e", "RELAYDOCK_PAYMENT_POLL_INTERVAL=250ms",
        "-e", "RELAYDOCK_GOVERNANCE_CLEANUP_INTERVAL=250ms",
        "-e", "RELAYDOCK_PAYMENT_ALLOWED_REGIONS=CN",
        "-e", "RELAYDOCK_PAYMENT_SANDBOX_ENABLED=true",
        "-e", "RELAYDOCK_PAYMENT_SANDBOX_SECRET=$sandboxSecret",
        "-e", "RELAYDOCK_PUBLIC_FUNNEL_RATE_LIMIT=120",
        "-e", "COOKIE_SECURE=false",
        $serverImage
    )
    [void](Invoke-Docker -Arguments $serverArguments -Operation "Starting the onboarding application")
    $containers.Add($server)

    $controlPort = Get-PublishedPort -Container $server -Port 8081
    $gatewayPort = Get-PublishedPort -Container $server -Port 8080
    $mockPort = Get-PublishedPort -Container $mock -Port 8090
    $controlURL = "http://127.0.0.1:$controlPort"
    $gatewayURL = "http://127.0.0.1:$gatewayPort"
    $mockURL = "http://127.0.0.1:$mockPort"
    try {
        Wait-HTTP -URL "$controlURL/readyz"
    } catch {
        $logs = [string]::Join("`n", @(& docker logs --tail 40 $server 2>&1))
        $logs = $logs -replace 'postgres://[^\s"]+', 'postgres://[redacted]' -replace 'redis://[^\s"]+', 'redis://[redacted]' -replace 'rdk_(live|test)_[A-Za-z0-9_-]+', 'rdk_$1_[redacted]'
        throw "Application did not become ready. Sanitized logs:`n$logs"
    }

    $config = Invoke-JSON -Method GET -URL "$controlURL/api/public/config"
    Assert-Status $config 200 "Reading public configuration"
    Assert-True ($config.JSON.product -eq "ModelDock" -and $config.JSON.compatibility_name -eq "RelayDock") "Public product compatibility identity is incorrect."
    Assert-True ($config.JSON.registration_mode -eq "PUBLIC" -and [bool]$config.JSON.email_verification_required) "Public registration/verification disclosure is incorrect."
    Assert-True ([bool]$config.JSON.legal_review_required) "Public configuration did not retain the counsel-review warning."
    Assert-True ($config.JSON.support_email -eq "helpdesk@example.invalid" -and $config.JSON.enterprise_email -eq "sales@example.invalid") "Runtime public contact mailboxes were not exposed through public configuration."

    $emptyPricing = Invoke-JSON -Method GET -URL "$controlURL/api/public/pricing?region=CN&currency=USD"
    Assert-Status $emptyPricing 200 "Reading unconfigured public pricing"
    Assert-True (-not [bool]$emptyPricing.JSON.terms_configured -and -not [bool]$emptyPricing.JSON.payment_fees_configured) "Unpublished commercial terms or fees were represented as configured."

    $anonymousID = "anon-$runID-$([Guid]::NewGuid().ToString('N'))"
    $homepageKey = "homepage-$runID"
    $homepageBody = @{ event_type = "HOMEPAGE_VISITED"; anonymous_id = $anonymousID; idempotency_key = $homepageKey }
    $homepage = Invoke-JSON -Method POST -URL "$controlURL/api/public/funnel/events" -Body $homepageBody
    Assert-Status $homepage 201 "Recording the homepage visit"
    $homepageReplay = Invoke-JSON -Method POST -URL "$controlURL/api/public/funnel/events" -Body $homepageBody
    Assert-Status $homepageReplay 200 "Replaying the homepage visit"
    Assert-True ([string]$homepage.JSON.event_id -eq [string]$homepageReplay.JSON.event_id -and [bool]$homepageReplay.JSON.replayed) "Homepage funnel idempotency did not return the original event."
    $anonymousPersistence = Invoke-Psql -SQL "SELECT count(*) FROM commercial_funnel_events WHERE metadata::text LIKE '%$anonymousID%' OR source_resource_id LIKE '%$anonymousID%';"
    Assert-True ([int]$anonymousPersistence -eq 0) "A raw anonymous identifier was persisted."

    $mailBefore = Get-MailFiles
    $userEmail = "onboarding-$runID@example.invalid"
    $userPassword = "synthetic-onboarding-password-2026"
    $registration = Invoke-JSON -Method POST -URL "$controlURL/api/console/auth/register" -Body @{ email = $userEmail; password = $userPassword; display_name = "Onboarding User" }
    Assert-Status $registration 202 "Registering the public user"
    $verificationToken = Wait-NewVerificationToken -Before $mailBefore
    $verification = Invoke-JSON -Method POST -URL "$controlURL/api/console/auth/verify-email" -Body @{ token = $verificationToken }
    Assert-Status $verification 200 "Verifying the public user"
    $userID = [string]$verification.JSON.user.id

    $consoleSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $consoleLogin = Invoke-JSON -Method POST -URL "$controlURL/api/console/auth/login" -Session $consoleSession -Body @{ email = $userEmail; password = $userPassword }
    Assert-Status $consoleLogin 200 "Signing in the public user"
    $consoleCSRF = Get-CSRFHeader -Session $consoleSession -BaseURL $controlURL -CookieName "relayedock_console_csrf"
    $projects = Invoke-SessionJSON -Method GET -URL "$controlURL/api/console/projects" -Session $consoleSession -CSRF $consoleCSRF
    Assert-Status $projects 200 "Listing the initial workspace"
    $project = @($projects.JSON.data) | Select-Object -First 1
    Assert-True ($null -ne $project) "Verified registration did not create an initial project."
    $projectID = [string]$project.id
    $organizationID = [string]$project.organization_id
    Assert-True (-not [string]::IsNullOrWhiteSpace($organizationID)) "The initial project did not identify its organization."

    $organizationBefore = Invoke-SessionJSON -Method GET -URL "$controlURL/api/console/organizations/$organizationID" -Session $consoleSession -CSRF $consoleCSRF
    Assert-Status $organizationBefore 200 "Reading the organization before region confirmation"
    $organizationUpdate = Invoke-SessionJSON -Method PUT -URL "$controlURL/api/console/organizations/$organizationID" -Session $consoleSession -CSRF $consoleCSRF -Body @{ billing_region = "CN" }
    Assert-Status $organizationUpdate 200 "Setting the organization's purchase region"
    Assert-True ($organizationUpdate.JSON.billing_region -eq "CN") "The organization purchase region was not persisted."
    foreach ($field in @("name", "slug", "status", "minimum_gross_margin")) {
        Assert-True ([string]$organizationUpdate.JSON.$field -eq [string]$organizationBefore.JSON.$field) "Region confirmation changed organization field $field."
    }
    foreach ($field in @("metadata", "allowed_provider_ids", "prohibited_provider_ids", "required_data_regions")) {
        $beforeProperty = $organizationBefore.JSON.PSObject.Properties[$field]
        $afterProperty = $organizationUpdate.JSON.PSObject.Properties[$field]
        $beforeValue = if ($null -eq $beforeProperty) { "<omitted>" } else { ConvertTo-Json $beforeProperty.Value -Compress -Depth 10 }
        $afterValue = if ($null -eq $afterProperty) { "<omitted>" } else { ConvertTo-Json $afterProperty.Value -Compress -Depth 10 }
        Assert-True ($afterValue -eq $beforeValue) "Region confirmation changed organization policy $field."
    }

    $adminSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $adminLogin = Invoke-JSON -Method POST -URL "$controlURL/api/admin/auth/login" -Session $adminSession -Body @{ email = "admin@example.invalid"; password = "synthetic-admin-password-2026" }
    Assert-Status $adminLogin 200 "Signing in the administrator"
    $adminCSRF = Get-CSRFHeader -Session $adminSession -BaseURL $controlURL -CookieName "relayedock_admin_csrf"

    $effectiveAt = [DateTime]::UtcNow.AddMinutes(-5).ToString("o")
    $termsBody = @{
        region = "CN"; currency = "USD"; subscription_tax_included = $false; token_tax_included = $false
        tax_disclosure = "Synthetic isolated-test disclosure: tax is not included."
        refund_summary = "Synthetic isolated-test disclosure: reviewed application required; bonus is non-refundable."
        refund_policy_url = "/legal/refunds"; bonus_credit_amount = "0.000000000000"; bonus_non_refundable = $true
        effective_at = $effectiveAt; legal_review_status = "APPROVED"; legal_review_confirmed = $true
        idempotency_key = "terms-$runID"
    }
    $terms = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/public/commercial-terms" -Session $adminSession -CSRF $adminCSRF -Body $termsBody
    Assert-Status $terms 201 "Publishing commercial disclosure evidence"
    $termsReplay = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/public/commercial-terms" -Session $adminSession -CSRF $adminCSRF -Body $termsBody
    Assert-Status $termsReplay 200 "Replaying commercial disclosure publication"
    Assert-True ([bool]$termsReplay.JSON.replayed -and [string]$termsReplay.JSON.commercial_terms.id -eq [string]$terms.JSON.commercial_terms.id) "Commercial terms idempotency did not return the original evidence."
    $conflictingTerms = $termsBody.Clone()
    $conflictingTerms.refund_summary = "Different synthetic refund disclosure."
    Assert-Status (Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/public/commercial-terms" -Session $adminSession -CSRF $adminCSRF -Body $conflictingTerms) 409 "Rejecting a conflicting commercial disclosure replay"

    $feeBody = @{
        fee_category = "PAYMENT_CHANNEL"; payment_provider = "sandbox"; region = "CN"; currency = "USD"
        fee_kind = "NONE"; fixed_amount = "0.000000000000"; rate_bps = 0; charged_to_customer = $false
        description = "Synthetic test-only channel; it moves no real funds."
        effective_at = $effectiveAt; legal_review_status = "APPROVED"; legal_review_confirmed = $true
        idempotency_key = "fee-$runID"
    }
    $fee = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/public/payment-fees" -Session $adminSession -CSRF $adminCSRF -Body $feeBody
    Assert-Status $fee 201 "Publishing payment fee evidence"
    $feeReplay = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/public/payment-fees" -Session $adminSession -CSRF $adminCSRF -Body $feeBody
    Assert-Status $feeReplay 200 "Replaying payment fee publication"
    Assert-True ([bool]$feeReplay.JSON.replayed -and [string]$feeReplay.JSON.payment_fee.id -eq [string]$fee.JSON.payment_fee.id) "Payment fee idempotency did not return the original evidence."

    $secondAdminPassword = "synthetic-second-admin-password-2026"
    $secondAdmin = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/users" -Session $adminSession -CSRF $adminCSRF -Body @{
        email = "second-admin-$runID@example.invalid"; password = $secondAdminPassword; display_name = "Second Pricing Reviewer"; role = "ADMIN"
    }
    Assert-Status $secondAdmin 201 "Creating an independent pricing reviewer"
    $secondAdminSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $secondAdminLogin = Invoke-JSON -Method POST -URL "$controlURL/api/admin/auth/login" -Session $secondAdminSession -Body @{ email = [string]$secondAdmin.JSON.email; password = $secondAdminPassword }
    Assert-Status $secondAdminLogin 200 "Signing in the independent pricing reviewer"
    $secondAdminCSRF = Get-CSRFHeader -Session $secondAdminSession -BaseURL $controlURL -CookieName "relayedock_admin_csrf"

    $provider = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/providers" -Session $adminSession -CSRF $adminCSRF -Body @{
        name = "Onboarding Mock $runID"; slug = "onboarding-mock-$runID"; provider_type = "openai"
        base_url = "http://$mock`:8090/v1"; enabled = $true; config = @{ test_only = $true }
        contract_status = "ACTIVE"; commercial_status = "COMMERCIAL_APPROVED"; commercial_resale_status = "APPROVED"
        legal_entity = "Synthetic local integration fixture"; contract_type = "TEST_ONLY"
        contract_start_at = $effectiveAt; contract_end_at = [DateTime]::UtcNow.AddDays(1).ToString("o")
        allowed_regions = @("CN"); allowed_customer_regions = @("CN"); prohibited_regions = @()
        data_processing_regions = @("CN"); data_retention_policy = "Synthetic test process lifetime only"
        terms_version = "synthetic-v1"; settlement_currency = "USD"; pricing_disabled = $false; emergency_kill_switch = $false
    }
    Assert-Status $provider 201 "Creating the contracted test Provider"
    $providerID = [string]$provider.JSON.id

    $primaryGroup = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/credential-groups" -Session $adminSession -CSRF $adminCSRF -Body @{
        provider_id = $providerID; name = "Onboarding Empty Primary $runID"; description = "Intentionally empty fallback verification group"
    }
    Assert-Status $primaryGroup 201 "Creating the empty primary Provider credential group"
    $group = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/credential-groups" -Session $adminSession -CSRF $adminCSRF -Body @{
        provider_id = $providerID; name = "Onboarding Group $runID"; description = "Synthetic authorized credential group"
    }
    Assert-Status $group 201 "Creating the Provider credential group"
    $credential = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/credentials" -Session $adminSession -CSRF $adminCSRF -Body @{
        provider_id = $providerID; name = "Onboarding Credential $runID"; secret = $mockAPIKey
        group_id = [string]$group.JSON.id; validate = $true; priority = 100; weight = 100; max_concurrency = 8
    }
    Assert-Status $credential 201 "Creating the authorized Provider credential"
    $sync = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/providers/$providerID/sync-models" -Session $adminSession -CSRF $adminCSRF -Body @{ credential_id = [string]$credential.JSON.id }
    Assert-Status $sync 200 "Synchronizing deterministic models"

    $adminModels = Invoke-SessionJSON -Method GET -URL "$controlURL/api/admin/models" -Session $adminSession -CSRF $adminCSRF
    Assert-Status $adminModels 200 "Reading synchronized models"
    $chatModel = @($adminModels.JSON.data | Where-Object { $_.provider_id -eq $providerID -and $_.provider_model_id -eq "mock-chat" }) | Select-Object -First 1
    Assert-True ($null -ne $chatModel) "The deterministic chat model was not synchronized."
    $modelID = [string]$chatModel.id

    $costChange = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/pricing/provider-cost-changes/manual" -Session $adminSession -CSRF $adminCSRF -Body @{
        provider_id = $providerID; model_id = $modelID; source_reference = "synthetic-contract-price"
        input_token_cost = "0.500000000000"; cached_input_token_cost = "0.250000000000"
        output_token_cost = "1.000000000000"; request_fixed_cost = "0.000000000000"
        currency = "USD"; unit = 1000000; effective_at = $effectiveAt; idempotency_key = "cost-$runID"
    }
    Assert-Status $costChange 202 "Submitting the Provider cost change"
    $costChangeID = [string]$costChange.JSON.change_request.id
    $costReview = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/pricing/provider-cost-changes/$costChangeID/review" -Session $secondAdminSession -CSRF $secondAdminCSRF -Body @{
        decision = "APPROVE"; reason = "Independent synthetic integration review"
    }
    Assert-Status $costReview 200 "Approving the Provider cost change with a second administrator"

    $retail = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/pricing/customer-retail-price-books" -Session $adminSession -CSRF $adminCSRF -Body @{
        provider_id = $providerID; model_id = $modelID
        input_token_price = "1.000000000000"; cached_input_token_price = "0.500000000000"
        output_token_price = "2.000000000000"; request_fixed_price = "0.000000000000"
        currency = "USD"; unit = 1000000; effective_at = $effectiveAt; source = "synthetic-public-retail"
    }
    Assert-Status $retail 201 "Publishing the public retail Token price"

    $routeAlias = "onboarding-chat-$runID"
    $route = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/model-routes" -Session $adminSession -CSRF $adminCSRF -Body @{
        alias = $routeAlias; provider_id = $providerID; upstream_model = "mock-chat"
        credential_group_id = [string]$primaryGroup.JSON.id; fallback_group_id = [string]$group.JSON.id
        routing_policy = "priority_weighted"; fallback_config = @{}
    }
    Assert-Status $route 201 "Creating the project model route"
    $routeGrant = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/projects/$projectID/routes" -Session $adminSession -CSRF $adminCSRF -Body @{
        model_route_id = [string]$route.JSON.id; enabled = $true; routing_config = @{}
    }
    Assert-Status $routeGrant 200 "Granting the model route to the initial project"

    $consoleModels = Invoke-SessionJSON -Method GET -URL "$controlURL/api/console/models?project_id=$projectID" -Session $consoleSession -CSRF $consoleCSRF
    Assert-Status $consoleModels 200 "Reading project aliases for public-catalog correlation"
    $consoleModel = @($consoleModels.JSON.data | Where-Object alias -eq $routeAlias) | Select-Object -First 1
    Assert-True ($null -ne $consoleModel -and [string]$consoleModel.provider_id -eq $providerID -and [string]$consoleModel.upstream_model -eq "mock-chat") "The authorized alias could not be correlated to its public Provider/model price."
    foreach ($field in @("provider_base_url", "credential_group_id", "fallback_group_id", "routing_config", "fallback_config")) {
        Assert-True ($null -eq $consoleModel.PSObject.Properties[$field]) "The Console model response exposed administrator-only routing field $field."
    }

    $publicProviders = Invoke-JSON -Method GET -URL "$controlURL/api/public/catalog/providers?region=CN"
    Assert-Status $publicProviders 200 "Reading the public Provider catalog"
    $publicProvider = @($publicProviders.JSON.items | Where-Object id -eq $providerID) | Select-Object -First 1
    Assert-True ($null -ne $publicProvider -and [bool]$publicProvider.availability.available) "The reviewed CN Provider was not shown as available."
    $deniedProviders = Invoke-JSON -Method GET -URL "$controlURL/api/public/catalog/providers?region=US"
    Assert-Status $deniedProviders 200 "Reading a disallowed regional Provider catalog"
    $deniedProvider = @($deniedProviders.JSON.items | Where-Object id -eq $providerID) | Select-Object -First 1
    Assert-True ($null -ne $deniedProvider -and -not [bool]$deniedProvider.availability.available -and $deniedProvider.availability.reason_code -eq "REGION_NOT_ALLOWED") "The disallowed Provider region was not disclosed before purchase."

    $publicModels = Invoke-JSON -Method GET -URL "$controlURL/api/public/catalog/models?region=CN&currency=USD"
    Assert-Status $publicModels 200 "Reading the public model catalog"
    $publicModel = @($publicModels.JSON.items | Where-Object id -eq $modelID) | Select-Object -First 1
    Assert-True ($null -ne $publicModel -and [bool]$publicModel.availability.available -and $null -ne $publicModel.pricing) "The model did not expose its actual available regional retail price."
    Assert-True ([string]$publicModel.pricing.input_token_price -eq "1.000000000000" -and [int64]$publicModel.pricing.unit -eq 1000000) "The public Token price does not match the approved retail price book."
    Assert-True ([bool]$publicModel.pricing.availability.available -and [string]$publicModel.pricing.availability.region -eq "CN") "The model price did not carry its nested regional availability."

    $publicPricing = Invoke-JSON -Method GET -URL "$controlURL/api/public/pricing?region=CN&currency=USD"
    Assert-Status $publicPricing 200 "Reading configured public pricing"
    Assert-True ([bool]$publicPricing.JSON.terms_configured -and [bool]$publicPricing.JSON.payment_fees_configured -and [bool]$publicPricing.JSON.payment_region_supported) "Published terms/fees or the enabled payment region were not marked configured."
    Assert-True ([string]$publicPricing.JSON.commercial_terms.bonus_credit_amount -eq "0.000000000000" -and [bool]$publicPricing.JSON.commercial_terms.bonus_non_refundable) "Bonus/refund disclosure is incomplete."
    Assert-True ($publicPricing.JSON.commercial_terms.legal_review_status -eq "APPROVED" -and @($publicPricing.JSON.payment_fees | Where-Object { $_.payment_provider -eq "sandbox" -and $_.legal_review_status -eq "APPROVED" }).Count -eq 1) "Self-service pricing was not backed by approved synthetic legal/fee evidence."
    $publicTokenPrice = @($publicPricing.JSON.token_prices | Where-Object model_id -eq $modelID) | Select-Object -First 1
    Assert-True ($null -ne $publicTokenPrice -and [bool]$publicTokenPrice.availability.available -and [string]$publicTokenPrice.availability.region -eq "CN") "Public pricing did not separate the approved Token price with regional availability."
    Assert-True (@($publicPricing.JSON.subscription_plans | Where-Object token_billing_mode -eq "METERED_SEPARATE").Count -gt 0) "Subscription plans did not disclose separate metered Token billing."

    $deniedPricing = Invoke-JSON -Method GET -URL "$controlURL/api/public/pricing?region=US&currency=USD"
    Assert-Status $deniedPricing 200 "Reading pricing for a disabled payment/model region"
    $deniedTokenPrice = @($deniedPricing.JSON.token_prices | Where-Object model_id -eq $modelID) | Select-Object -First 1
    Assert-True (-not [bool]$deniedPricing.JSON.payment_region_supported -and $null -ne $deniedTokenPrice -and -not [bool]$deniedTokenPrice.availability.available) "Pricing did not disclose the disabled payment/model region before purchase."

    $onboardingBefore = Invoke-SessionJSON -Method GET -URL "$controlURL/api/console/onboarding?organization_id=$organizationID&project_id=$projectID" -Session $consoleSession -CSRF $consoleCSRF
    Assert-Status $onboardingBefore 200 "Reading initial onboarding evidence"
    Assert-True ([string]$onboardingBefore.JSON.project_id -eq $projectID) "Onboarding evidence was not scoped to the requested project."
    Assert-True ($onboardingBefore.JSON.next_step -eq "SELECT_PLAN") "The initial server-derived next step was not explicit plan selection."

    $developerPlan = @($publicPricing.JSON.subscription_plans | Where-Object slug -eq "developer") | Select-Object -First 1
    Assert-True ($null -ne $developerPlan) "The published Developer plan was not available for onboarding."
    $subscriptionChange = Invoke-SessionJSON -Method POST -URL "$controlURL/api/console/organizations/$organizationID/subscription/change" -Session $consoleSession -CSRF $consoleCSRF -Body @{
        plan_version_id = [string]$developerPlan.plan_version_id; mode = "IMMEDIATE"; use_trial = $true
        idempotency_key = "subscription-$runID"; metadata = @{ source = "commercial-onboarding-integration" }
    }
    Assert-Status $subscriptionChange 200 "Selecting the Developer plan"
    Assert-True ($subscriptionChange.JSON.token_billing_mode -eq "METERED_SEPARATE") "Plan selection did not preserve separate Token metering."

    $recharge = Invoke-SessionJSON -Method POST -URL "$controlURL/api/console/organizations/$organizationID/recharge-orders" -Session $consoleSession -CSRF $consoleCSRF -Body @{
        amount = "10.000000000000"; currency = "USD"; region = "CN"; payment_provider = "sandbox"; idempotency_key = "recharge-$runID"
    }
    Assert-Status $recharge 201 "Creating the test-only recharge order"
    Assert-True ($recharge.JSON.payment.instructions.mode -eq "sandbox") "The test-only payment channel was not clearly disclosed."
    $platformOrderNo = [string]$recharge.JSON.order.platform_order_no
    $providerOrderNo = [string]$recharge.JSON.order.provider_order_no
    $paymentEventID = "payment-$runID"
    $paymentTimestamp = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds().ToString()
    $paymentBody = ConvertTo-Json ([ordered]@{
        event_id = $paymentEventID; event_type = "payment.succeeded"; platform_order_no = $platformOrderNo
        provider_order_no = $providerOrderNo; status = "PAID"; amount = "10.000000000000"; currency = "USD"
    }) -Compress
    $paymentSignature = Get-HMACSHA256Hex -Secret $sandboxSecret -Value "$paymentTimestamp.$paymentBody"
    $paymentWebhook = Invoke-RawJSON -Method POST -URL "$controlURL/api/payments/webhooks/sandbox" -Body $paymentBody -Headers @{
        "X-Payment-Timestamp" = $paymentTimestamp; "X-Payment-Event-Id" = $paymentEventID; "X-Payment-Signature" = $paymentSignature
    }
    Assert-Status $paymentWebhook 200 "Processing the signed test-only payment webhook"
    Assert-True ($paymentWebhook.JSON.order_status -eq "CREDITED") "Verified recharge was not atomically credited."
    $rechargeID = [string]$recharge.JSON.order.id
    $paymentWebhookReplay = Invoke-RawJSON -Method POST -URL "$controlURL/api/payments/webhooks/sandbox" -Body $paymentBody -Headers @{
        "X-Payment-Timestamp" = $paymentTimestamp; "X-Payment-Event-Id" = $paymentEventID; "X-Payment-Signature" = $paymentSignature
    }
    Assert-Status $paymentWebhookReplay 200 "Replaying the signed payment webhook"
    $paymentReplayEvidence = Invoke-Psql -SQL "SELECT concat_ws('|',(SELECT count(*) FROM payment_webhook_event WHERE provider_event_id='$paymentEventID'),(SELECT count(*) FROM wallet_transactions WHERE recharge_order_id='$rechargeID' AND transaction_type='TOPUP'));"
    Assert-True ($paymentReplayEvidence -eq "1|1") "Payment webhook replay created duplicate durable evidence or wallet credit."

    $promotion = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/pricing/promotion-credits" -Session $adminSession -CSRF $adminCSRF -Body @{
        organization_id = $organizationID; currency = "USD"; amount_granted = "0.000005000000"
        source = "synthetic-go-live-discount"; idempotency_key = "promotion-$runID"
    }
    Assert-Status $promotion 201 "Granting an isolated non-refundable promotion credit"

    $keyResponse = Invoke-SessionJSON -Method POST -URL "$controlURL/api/console/api-keys" -Session $consoleSession -CSRF $consoleCSRF -Body @{
        project_id = $projectID; name = "Onboarding key $runID"; environment = "test"
        rate_limit_rpm = 60; rate_limit_tpm = 100000; allowed_models = @($routeAlias)
    }
    Assert-Status $keyResponse 201 "Creating the one-time project API key"
    $apiKey = [string]$keyResponse.JSON.key
    Assert-True ($apiKey.StartsWith("rdk_test_")) "The existing rdk_test_* API key contract changed."

    $modelsWithKey = Invoke-JSON -Method GET -URL "$gatewayURL/v1/models" -Headers @{ Authorization = "Bearer $apiKey" }
    Assert-Status $modelsWithKey 200 "Listing models with the RelayDock-compatible key"
    Assert-True (@($modelsWithKey.JSON.data | Where-Object id -eq $routeAlias).Count -eq 1) "The granted route was not visible through /v1/models."

    $firstCall = Invoke-JSON -Method POST -URL "$gatewayURL/v1/responses" -Headers @{
        Authorization = "Bearer $apiKey"; "Idempotency-Key" = "first-call-$runID"
    } -Body @{ model = $routeAlias; input = "Return the word ready."; max_output_tokens = 16 }
    Assert-Status $firstCall 200 "Sending the first OpenAI-compatible request"
    $firstCallReplay = Invoke-JSON -Method POST -URL "$gatewayURL/v1/responses" -Headers @{
        Authorization = "Bearer $apiKey"; "Idempotency-Key" = "first-call-$runID"
    } -Body @{ model = $routeAlias; input = "Return the word ready."; max_output_tokens = 16 }
    Assert-Status $firstCallReplay 409 "Replaying the gateway Idempotency-Key"
    $secondCall = Invoke-JSON -Method POST -URL "$gatewayURL/v1/responses" -Headers @{
        Authorization = "Bearer $apiKey"; "Idempotency-Key" = "second-call-$runID"
    } -Body @{ model = $routeAlias; input = "Return the word again."; max_output_tokens = 16 }
    Assert-Status $secondCall 200 "Sending the second OpenAI-compatible request"

    $fundingEvidence = Invoke-Psql -SQL @"
SELECT concat_ws('|',
  (SELECT count(*) FROM funding_operation WHERE organization_id='$organizationID' AND idempotency_key='first-call-$runID'),
  (SELECT count(*) FROM funding_operation operation JOIN usage_price_snapshot snapshot ON snapshot.request_id=operation.request_id
    JOIN funding_provider_attempt attempt ON attempt.operation_id=operation.id
    WHERE operation.idempotency_key='first-call-$runID' AND operation.status='SETTLED'
      AND operation.settled_amount=0.000009 AND operation.released_amount>0
      AND operation.consumed_promotion_amount=0.000005 AND snapshot.provider_cost_amount=0.000007
      AND snapshot.customer_sale_amount=0.000014 AND snapshot.promotion_amount=0.000005
      AND snapshot.final_user_amount=0.000009 AND snapshot.platform_gross_margin=0.000007 AND attempt.is_fallback),
  (SELECT count(*) FROM funding_operation operation JOIN usage_price_snapshot snapshot ON snapshot.request_id=operation.request_id
    WHERE operation.idempotency_key='second-call-$runID' AND operation.status='SETTLED'
      AND operation.settled_amount=0.000014 AND operation.released_amount>0
      AND operation.consumed_promotion_amount=0 AND snapshot.provider_cost_amount=0.000007
      AND snapshot.customer_sale_amount=0.000014 AND snapshot.final_user_amount=0.000014));
"@
    Assert-True ($fundingEvidence -eq "1|1|1") "Exact reservation, settlement, release, Provider cost, retail, promotion, margin, fallback, or request replay evidence is incorrect."

    $monthlyStatements = Invoke-SessionJSON -Method GET -URL "$controlURL/api/console/organizations/$organizationID/finance/monthly-statements" -Session $consoleSession -CSRF $consoleCSRF
    Assert-Status $monthlyStatements 200 "Reading the monthly customer statement"
    $currentMonth = [DateTime]::UtcNow.ToString("yyyy-MM")
    Assert-True (@($monthlyStatements.JSON.data | Where-Object { $_.month -eq $currentMonth -and [int64]$_.request_count -ge 2 }).Count -eq 1) "The current monthly statement did not include the settled requests."

    $paymentReconciliation = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/recharge-orders/$rechargeID/reconcile" -Session $adminSession -CSRF $adminCSRF -Body @{}
    Assert-Status $paymentReconciliation 201 "Reconciling sandbox payment channel evidence"

    $usageDate = [DateTime]::UtcNow.ToString("yyyy-MM-dd")
    $traceRows = @((Invoke-Psql -SQL "SELECT operation.request_id||'|'||attempt.upstream_request_id FROM funding_operation operation JOIN funding_provider_attempt attempt ON attempt.operation_id=operation.id WHERE operation.idempotency_key IN ('first-call-$runID','second-call-$runID') AND attempt.status='SUCCEEDED' ORDER BY operation.idempotency_key;").Split("`n") | Where-Object { $_ })
    Assert-True ($traceRows.Count -eq 2) "The successful requests did not produce two Provider trace identifiers."
    $statementLines = @()
    for ($lineIndex = 0; $lineIndex -lt $traceRows.Count; $lineIndex++) {
        $trace = $traceRows[$lineIndex].Split("|", 2)
        $statementLines += @{
            external_line_id = "line-$runID-$lineIndex"; usage_date = "$usageDate`T00:00:00Z"
            request_id = $trace[0]; upstream_request_id = $trace[1]; amount = "0.000007000000"; currency = "USD"
            input_tokens = 4; cached_input_tokens = 0; output_tokens = 5; metadata = @{ source = "synthetic-go-live" }
        }
    }
    $providerStatement = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/finance/provider-statements" -Session $adminSession -CSRF $adminCSRF -Body @{
        provider_id = $providerID; statement_reference = "statement-$runID"; period_start = $usageDate; period_end = $usageDate
        region = "CN"; currency = "USD"; total_amount = "0.000014000000"; source_sha256 = ("a" * 64); lines = $statementLines
    }
    Assert-Status $providerStatement 201 "Importing isolated Provider billing evidence"
    $reconciliationRun = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/finance/reconciliation/runs" -Session $adminSession -CSRF $adminCSRF -Body @{ business_date = $usageDate }
    if ($reconciliationRun.Status -ne 201) {
        $reconciliationDiagnostic = Invoke-Psql -SQL "SELECT status||'|'||COALESCE(error_code,'') FROM financial_reconciliation_run WHERE business_date='$usageDate' ORDER BY started_at DESC LIMIT 1;"
        $savedPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "Continue"
            $reconciliationLogs = [string]::Join("`n", @(& docker logs --tail 30 $server 2>&1))
        } finally {
            $ErrorActionPreference = $savedPreference
        }
        $reconciliationLogs = $reconciliationLogs -replace 'postgres://[^\s"]+', 'postgres://[redacted]' -replace 'redis://[^\s"]+', 'redis://[redacted]' -replace 'rdk_(live|test)_[A-Za-z0-9_-]+', 'rdk_$1_[redacted]'
        throw "Four-way reconciliation failed ($reconciliationDiagnostic). Sanitized logs:`n$reconciliationLogs"
    }
    Assert-Status $reconciliationRun 201 "Running payment, wallet, usage, and Provider reconciliation"
    if ($reconciliationRun.JSON.status -ne "COMPLETED" -or [int]$reconciliationRun.JSON.summary.differences -ne 0) {
        $differenceDiagnostic = Invoke-Psql -SQL "SELECT check_type||'|'||classification||'|'||case_key FROM financial_reconciliation_case WHERE first_seen_run_id='$([string]$reconciliationRun.JSON.id)' ORDER BY check_type,case_key;"
        throw "The payment/wallet/usage/Provider reconciliation produced a difference: $differenceDiagnostic"
    }

    $streamHTTP = Invoke-WebRequest -UseBasicParsing -Uri "$gatewayURL/v1/responses" -Method POST -TimeoutSec 20 -ContentType "application/json" -Headers @{
        Authorization = "Bearer $apiKey"; "Idempotency-Key" = "stream-call-$runID"
    } -Body (@{ model = $routeAlias; input = "Stream the word ready."; max_output_tokens = 16; stream = $true } | ConvertTo-Json -Compress)
    Assert-True ([int]$streamHTTP.StatusCode -eq 200 -and [string]$streamHTTP.Headers["Content-Type"] -like "text/event-stream*" -and [string]$streamHTTP.Content -match "response.completed") "The OpenAI-compatible SSE response was incomplete."
    [void](Wait-PsqlValue -SQL "SELECT count(*) FROM funding_operation WHERE idempotency_key='stream-call-$runID' AND status='SETTLED' AND settled_amount>0 AND released_amount>0;" -Expected "1" -Operation "SSE funding settlement")

    $clientAbortScenario = Invoke-JSON -Method POST -URL "$mockURL/__test/scenario" -Headers @{ "X-RelayDock-Test-Token" = $mockTestToken } -Body @{ chunk_delay_ms = 750; once = $true }
    Assert-Status $clientAbortScenario 200 "Configuring a partial SSE client-abort scenario"
    $abortBody = @{ model = $routeAlias; input = "Abort this stream after partial output."; max_output_tokens = 16; stream = $true } | ConvertTo-Json -Compress
    Add-Type -AssemblyName System.Net.Http
    $abortClient = [Net.Http.HttpClient]::new()
    $abortRequest = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Post, "$gatewayURL/v1/responses")
    $abortRequest.Headers.Authorization = [Net.Http.Headers.AuthenticationHeaderValue]::new("Bearer", $apiKey)
    [void]$abortRequest.Headers.TryAddWithoutValidation("Idempotency-Key", "client-abort-$runID")
    $abortRequest.Content = [Net.Http.StringContent]::new($abortBody, [Text.Encoding]::UTF8, "application/json")
    $abortCancellation = [Threading.CancellationTokenSource]::new()
    $abortCancellation.CancelAfter(1000)
    $abortCancelled = $false
    $abortResponse = $null
    $abortStream = $null
    try {
        $abortResponse = $abortClient.SendAsync($abortRequest, [Net.Http.HttpCompletionOption]::ResponseHeadersRead, $abortCancellation.Token).GetAwaiter().GetResult()
        Assert-True ([int]$abortResponse.StatusCode -eq 200) "The client-abort fixture did not receive successful SSE headers."
        $abortStream = $abortResponse.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
        $abortBuffer = New-Object byte[] 4096
        while ($true) {
            $read = $abortStream.ReadAsync($abortBuffer, 0, $abortBuffer.Length, $abortCancellation.Token).GetAwaiter().GetResult()
            if ($read -eq 0) { break }
        }
    } catch [OperationCanceledException] {
        $abortCancelled = $true
    } finally {
        if ($null -ne $abortStream) { $abortStream.Dispose() }
        if ($null -ne $abortResponse) { $abortResponse.Dispose() }
        $abortRequest.Dispose()
        $abortClient.Dispose()
        $abortCancellation.Dispose()
    }
    Assert-True $abortCancelled "The synthetic client did not cancel the successful SSE request at its one-second deadline."
    [void](Wait-PsqlValue -SQL "SELECT count(*) FROM funding_operation WHERE idempotency_key='client-abort-$runID' AND status IN('PARTIALLY_SETTLED','SETTLED','RELEASED','FAILED');" -Expected "1" -Operation "client-disconnect terminal settlement")
    $abortEvidence = Invoke-Psql -SQL "SELECT operation.status||'|'||operation.settled_amount::text||'|'||operation.released_amount::text||'|'||COALESCE(log.error_code,'') FROM funding_operation operation LEFT JOIN request_logs log ON log.request_id=operation.request_id WHERE operation.idempotency_key='client-abort-$runID';"
    Assert-True ($abortEvidence.EndsWith("|client_disconnected")) "The gateway did not classify the cancelled SSE request correctly: $abortEvidence"
    Assert-True ((Invoke-Psql -SQL "SELECT count(*) FROM wallets WHERE organization_id='$organizationID' AND reserved_balance=0;") -eq "1") "Client interruption left wallet funds reserved."
    Wait-RedisCounterCleared -Container $redis -Key "rdk:subscription:active:$organizationID" -Operation "client-disconnect concurrency release"

    $gatewayJob = {
        param($URL, $Key, $Model, $Idempotency, $Stream)
        $body = @{ model = $Model; input = "Concurrent synthetic request"; max_output_tokens = 16; stream = $Stream } | ConvertTo-Json -Compress
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri "$URL/v1/responses" -Method POST -TimeoutSec 30 -ContentType "application/json" -Headers @{
                Authorization = "Bearer $Key"; "Idempotency-Key" = $Idempotency
            } -Body $body
            return [int]$response.StatusCode
        } catch {
            if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
            return -1
        }
    }
    foreach ($streamMode in @($false, $true)) {
        $kind = if ($streamMode) { "stream" } else { "normal" }
        $scenario = Invoke-JSON -Method POST -URL "$mockURL/__test/scenario" -Headers @{ "X-RelayDock-Test-Token" = $mockTestToken } -Body @{ delay_ms = 200 }
        Assert-Status $scenario 200 "Configuring concurrent $kind requests"
        $jobs = @()
        try {
            $jobs = @(0..7 | ForEach-Object { Start-Job -ScriptBlock $gatewayJob -ArgumentList $gatewayURL, $apiKey, $routeAlias, "concurrent-$kind-$runID-$_", $streamMode })
            $completed = @($jobs | Wait-Job -Timeout 40)
            Assert-True ($completed.Count -eq 8) "Concurrent $kind requests did not finish."
            $statuses = @($jobs | Receive-Job | ForEach-Object { [int]$_ })
            Assert-True (@($statuses | Where-Object { $_ -eq 200 }).Count -eq 8) "A concurrent $kind request failed: $([string]::Join(',', $statuses))."
        } finally {
            $jobs | Stop-Job -ErrorAction SilentlyContinue
            $jobs | Remove-Job -Force -ErrorAction SilentlyContinue
        }
        $scenarioReset = Invoke-JSON -Method POST -URL "$mockURL/__test/scenario" -Headers @{ "X-RelayDock-Test-Token" = $mockTestToken } -Body @{}
        Assert-Status $scenarioReset 200 "Resetting the concurrent Provider scenario"
        [void](Wait-PsqlValue -SQL "SELECT count(*) FROM funding_operation WHERE idempotency_key LIKE 'concurrent-$kind-$runID-%' AND status='SETTLED';" -Expected "8" -Operation "concurrent $kind settlements")
        Assert-True ((Invoke-Psql -SQL "SELECT count(*) FROM wallets WHERE organization_id='$organizationID' AND reserved_balance=0;") -eq "1") "Concurrent $kind requests left wallet funds reserved."
    }

    $switchScenario = Invoke-JSON -Method POST -URL "$mockURL/__test/scenario" -Headers @{ "X-RelayDock-Test-Token" = $mockTestToken } -Body @{ delay_ms = 1500; once = $true }
    Assert-Status $switchScenario 200 "Configuring the in-flight price switch"
    $switchJob = Start-Job -ScriptBlock $gatewayJob -ArgumentList $gatewayURL, $apiKey, $routeAlias, "price-switch-$runID", $false
    try {
        [void](Wait-PsqlValue -SQL "SELECT count(*) FROM funding_operation WHERE idempotency_key='price-switch-$runID' AND status='RESERVED';" -Expected "1" -Operation "in-flight price reservation")
        $retailSwitch = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/pricing/customer-retail-price-books" -Session $adminSession -CSRF $adminCSRF -Body @{
            provider_id = $providerID; model_id = $modelID
            input_token_price = "3.000000000000"; cached_input_token_price = "1.500000000000"
            output_token_price = "6.000000000000"; request_fixed_price = "0.000000000000"
            currency = "USD"; unit = 1000000; effective_at = [DateTime]::UtcNow.AddSeconds(-1).ToString("o"); source = "synthetic-inflight-switch"
        }
        Assert-Status $retailSwitch 201 "Publishing a new retail price while a request is in flight"
        $switchedQuote = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/pricing/quotes" -Session $adminSession -CSRF $adminCSRF -Body @{
            organization_id = $organizationID; provider_id = $providerID; model = "mock-chat"
            estimated_input_tokens = 4; estimated_cached_input_tokens = 0; estimated_output_tokens = 5
            tax_rate = "0"; exchange_rate = "1"; idempotency_key = "price-switch-quote-$runID"
        }
        Assert-Status $switchedQuote 200 "Materializing the newly published retail price version"
        $null = $switchJob | Wait-Job -Timeout 30
        $switchStatus = @($switchJob | Receive-Job | ForEach-Object { [int]$_ }) | Select-Object -First 1
        Assert-True ($switchStatus -eq 200) "The in-flight request did not finish after the price switch."
    } finally {
        $switchJob | Stop-Job -ErrorAction SilentlyContinue
        $switchJob | Remove-Job -Force -ErrorAction SilentlyContinue
    }
    $frozenPriceEvidence = Invoke-Psql -SQL "SELECT count(*) FROM funding_operation operation JOIN usage_price_snapshot snapshot ON snapshot.request_id=operation.request_id JOIN model_price_version latest ON latest.id=(SELECT id FROM model_price_version WHERE provider_id='$providerID' AND model_id='$modelID' ORDER BY effective_at DESC,created_at DESC LIMIT 1) WHERE operation.idempotency_key='price-switch-$runID' AND snapshot.retail_input_token_price=1 AND latest.retail_input_token_price=3 AND snapshot.pricing_version_id<>latest.id;"
    if ($frozenPriceEvidence -ne "1") {
        $frozenPriceDiagnostic = Invoke-Psql -SQL "SELECT concat_ws('|',operation.status,operation.pricing_version_id,snapshot.pricing_version_id,snapshot.retail_input_token_price::text,latest.id,latest.retail_input_token_price::text) FROM funding_operation operation LEFT JOIN usage_price_snapshot snapshot ON snapshot.request_id=operation.request_id CROSS JOIN LATERAL(SELECT id,retail_input_token_price FROM model_price_version WHERE provider_id='$providerID' AND model_id='$modelID' ORDER BY effective_at DESC,created_at DESC LIMIT 1) latest WHERE operation.idempotency_key='price-switch-$runID';"
        throw "The in-flight request did not retain its admitted immutable price version: $frozenPriceDiagnostic"
    }

    foreach ($fault in @(
        @{ Name = "timeout"; Scenario = @{ delay_ms = 3000; once = $true }; Expected = 502 },
        @{ Name = "429"; Scenario = @{ status = 429; retry_after = 1; once = $true }; Expected = 429 },
        @{ Name = "500"; Scenario = @{ status = 500; once = $true }; Expected = 500 }
    )) {
        $faultScenario = Invoke-JSON -Method POST -URL "$mockURL/__test/scenario" -Headers @{ "X-RelayDock-Test-Token" = $mockTestToken } -Body $fault.Scenario
        Assert-Status $faultScenario 200 "Configuring Provider $($fault.Name)"
        $faultResponse = Invoke-JSON -Method POST -URL "$gatewayURL/v1/responses" -Headers @{
            Authorization = "Bearer $apiKey"; "Idempotency-Key" = "provider-$($fault.Name)-$runID"
        } -Body @{ model = $routeAlias; input = "Provider fault injection"; max_output_tokens = 16 }
        Assert-Status $faultResponse ([int]$fault.Expected) "Handling Provider $($fault.Name)"
        [void](Wait-PsqlValue -SQL "SELECT count(*) FROM funding_operation WHERE idempotency_key='provider-$($fault.Name)-$runID' AND status='FAILED' AND settled_amount=0 AND released_amount=maximum_amount;" -Expected "1" -Operation "Provider $($fault.Name) funding release")
        $credentialReset = Invoke-SessionJSON -Method PATCH -URL "$controlURL/api/admin/credentials/$([string]$credential.JSON.id)/status" -Session $adminSession -CSRF $adminCSRF -Body @{ status = "ACTIVE" }
        Assert-Status $credentialReset 200 "Resetting the isolated Provider credential after $($fault.Name)"
    }

    $redisStatus = ""
    try {
        [void](Invoke-Docker -Arguments @("pause", $redis) -Operation "Pausing isolated Redis")
        $redisResponse = Invoke-JSON -Method POST -URL "$gatewayURL/v1/responses" -Headers @{
            Authorization = "Bearer $apiKey"; "Idempotency-Key" = "redis-outage-$runID"
        } -Body @{ model = $routeAlias; input = "Redis outage"; max_output_tokens = 16 }
        $redisStatus = [string]$redisResponse.Status
    } finally {
        [void](Invoke-Docker -Arguments @("unpause", $redis) -Operation "Unpausing isolated Redis")
        Wait-ContainerCommand -Container $redis -Command @("redis-cli", "ping") -Operation "Redis recovery"
    }
    Assert-True ($redisStatus -eq "503") "Redis unavailability did not fail the request closed with HTTP 503 (actual=$redisStatus)."
    Start-Sleep -Milliseconds 500
    $redisFundingInvalid = Invoke-Psql -SQL "SELECT count(*) FROM funding_operation WHERE idempotency_key='redis-outage-$runID' AND NOT(status IN('FAILED','RELEASED') AND settled_amount=0 AND released_amount=maximum_amount);"
    $redisFundingDiagnostic = Invoke-Psql -SQL "SELECT concat_ws('|',COALESCE(operation.status,'NO_OPERATION'),COALESCE(operation.settled_amount::text,'0'),COALESCE(operation.released_amount::text,'0'),wallet.reserved_balance::text,COALESCE(log.status_code::text,''),COALESCE(log.error_code,''),COALESCE(log.usage_source,''),COALESCE(attempt.status,''),COALESCE(attempt.http_status::text,'')) FROM wallets wallet LEFT JOIN funding_operation operation ON operation.organization_id=wallet.organization_id AND operation.idempotency_key='redis-outage-$runID' LEFT JOIN request_logs log ON log.request_id=operation.request_id LEFT JOIN LATERAL(SELECT status,http_status FROM funding_provider_attempt WHERE operation_id=operation.id ORDER BY attempt_no DESC LIMIT 1) attempt ON true WHERE wallet.organization_id='$organizationID';"
    $redisFundingParts = $redisFundingDiagnostic.Split("|")
    Assert-True ($redisFundingInvalid -eq "0" -and [decimal]$redisFundingParts[3] -eq 0) "Redis unavailability left a non-terminal funding operation or reserved wallet balance: $redisFundingDiagnostic"
    Assert-Status (Invoke-JSON -Method POST -URL "$gatewayURL/v1/responses" -Headers @{ Authorization = "Bearer $apiKey"; "Idempotency-Key" = "redis-recovered-$runID" } -Body @{ model = $routeAlias; input = "Redis recovered"; max_output_tokens = 16 }) 200 "Sending a request after Redis recovery"

    $databaseStatus = ""
    try {
        [void](Invoke-Docker -Arguments @("pause", $postgres) -Operation "Pausing isolated PostgreSQL")
        $databaseStatus = [string]::Join("", @(& curl.exe --silent --output NUL --write-out "%{http_code}" --max-time 4 "$controlURL/readyz" 2>$null))
    } finally {
        [void](Invoke-Docker -Arguments @("unpause", $postgres) -Operation "Unpausing isolated PostgreSQL")
        Wait-ContainerCommand -Container $postgres -Command @("pg_isready", "--username", "relaydock", "--dbname", "relaydock") -Operation "PostgreSQL recovery"
    }
    Assert-True ($databaseStatus -ne "200") "Readiness incorrectly stayed successful while the database was unavailable."
    Wait-HTTP -URL "$controlURL/readyz"

    $refundRecharge = Invoke-SessionJSON -Method POST -URL "$controlURL/api/console/organizations/$organizationID/recharge-orders" -Session $consoleSession -CSRF $consoleCSRF -Body @{
        amount = "2.000000000000"; currency = "USD"; region = "CN"; payment_provider = "sandbox"; idempotency_key = "refund-recharge-$runID"
    }
    Assert-Status $refundRecharge 201 "Creating the isolated refundable recharge"
    $refundOrderID = [string]$refundRecharge.JSON.order.id
    $refundPaymentTimestamp = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds().ToString()
    $refundPaymentEventID = "refund-payment-$runID"
    $refundPaymentBody = ConvertTo-Json ([ordered]@{
        event_id = $refundPaymentEventID; event_type = "payment.succeeded"; platform_order_no = [string]$refundRecharge.JSON.order.platform_order_no
        provider_order_no = [string]$refundRecharge.JSON.order.provider_order_no; status = "PAID"; amount = "2.000000000000"; currency = "USD"
    }) -Compress
    $refundPaymentSignature = Get-HMACSHA256Hex -Secret $sandboxSecret -Value "$refundPaymentTimestamp.$refundPaymentBody"
    Assert-Status (Invoke-RawJSON -Method POST -URL "$controlURL/api/payments/webhooks/sandbox" -Body $refundPaymentBody -Headers @{
        "X-Payment-Timestamp" = $refundPaymentTimestamp; "X-Payment-Event-Id" = $refundPaymentEventID; "X-Payment-Signature" = $refundPaymentSignature
    }) 200 "Crediting the isolated refundable recharge"
    $refundBody = @{ amount = "2.000000000000"; reason = "Synthetic go-live refund"; idempotency_key = "refund-$runID" }
    $refund = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/recharge-orders/$refundOrderID/refunds" -Session $adminSession -CSRF $adminCSRF -ExtraHeaders @{ "Idempotency-Key" = "refund-$runID" } -Body $refundBody
    Assert-Status $refund 201 "Completing the sandbox refund"
    $refundReplay = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/recharge-orders/$refundOrderID/refunds" -Session $adminSession -CSRF $adminCSRF -ExtraHeaders @{ "Idempotency-Key" = "refund-$runID" } -Body $refundBody
    Assert-Status $refundReplay 200 "Replaying the sandbox refund"
    Assert-True ((Invoke-Psql -SQL "SELECT count(*) FROM refund_order WHERE recharge_order_id='$refundOrderID' AND idempotency_key='refund-$runID' AND status='SUCCEEDED';") -eq "1") "Refund replay created duplicate or incomplete refund evidence."

    $isolatedOrganization = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/organizations" -Session $adminSession -CSRF $adminCSRF -Body @{
        name = "Isolation Organization $runID"; slug = "isolation-org-$runID"; status = "ACTIVE"; metadata = @{ test_run = $runID }
    }
    Assert-Status $isolatedOrganization 201 "Creating a cross-organization authorization fixture"
    Assert-Status (Invoke-SessionJSON -Method GET -URL "$controlURL/api/console/organizations/$([string]$isolatedOrganization.JSON.id)" -Session $consoleSession -CSRF $consoleCSRF) 404 "Rejecting a cross-organization read"

    $privacy = Invoke-SessionJSON -Method PUT -URL "$controlURL/api/console/privacy/USER/$userID" -Session $consoleSession -CSRF $consoleCSRF -Body @{
        save_content = $false; retention_days = 1; cross_border_route = "DOMESTIC"; legal_hold = $false
    }
    Assert-Status $privacy 200 "Setting the isolated retention policy"
    $report = Invoke-SessionJSON -Method POST -URL "$controlURL/api/console/reports" -Session $consoleSession -CSRF $consoleCSRF -Body @{
        organization_id = $organizationID; report_type = "OTHER"; description = "Synthetic retention cleanup evidence"
    }
    Assert-Status $report 201 "Creating a retention cleanup fixture"
    $reportID = [string]$report.JSON.id
    $reportResolved = Invoke-SessionJSON -Method PATCH -URL "$controlURL/api/admin/reports/$reportID" -Session $adminSession -CSRF $adminCSRF -Body @{ status = "RESOLVED"; resolution = "Synthetic resolution" }
    Assert-Status $reportResolved 204 "Resolving the retention cleanup fixture"
    [void](Invoke-Psql -SQL "UPDATE user_reports SET created_at=now()-interval '2 days',updated_at=now()-interval '2 days' WHERE id='$reportID';")
    [void](Wait-PsqlValue -SQL "SELECT count(*) FROM user_reports WHERE id='$reportID' AND description='[redacted by retention policy]';" -Expected "1" -Operation "governance retention cleanup")

    $backupPath = "/tmp/modeldock-go-live-$runID.dump"
    $restoreDatabase = "modeldock_restore_$runID"
    [void](Invoke-Docker -Arguments @("exec", $postgres, "pg_dump", "--username", "postgres", "--format=custom", "--file=$backupPath", "relaydock") -Operation "Creating the isolated PostgreSQL backup")
    [void](Invoke-Docker -Arguments @("exec", $postgres, "createdb", "--username", "postgres", $restoreDatabase) -Operation "Creating the isolated restore target")
    [void](Invoke-Docker -Arguments @("exec", $postgres, "pg_restore", "--username", "postgres", "--dbname", $restoreDatabase, "--no-owner", $backupPath) -Operation "Restoring the isolated PostgreSQL backup")
    $restoreEvidence = [string]::Join("`n", (Invoke-Docker -Arguments @("exec", $postgres, "psql", "--no-psqlrc", "--tuples-only", "--no-align", "--username", "postgres", "--dbname", $restoreDatabase, "--command", "SELECT concat_ws('|',(SELECT max(version) FROM schema_migrations),(SELECT count(*) FROM funding_operation),(SELECT count(*) FROM ledger_journal WHERE status='POSTED'),(SELECT count(*) FROM audit_logs));") -Operation "Validating the restored database")).Trim()
    $liveEvidence = Invoke-Psql -SQL "SELECT concat_ws('|',(SELECT max(version) FROM schema_migrations),(SELECT count(*) FROM funding_operation),(SELECT count(*) FROM ledger_journal WHERE status='POSTED'),(SELECT count(*) FROM audit_logs));"
    Assert-True ($restoreEvidence -eq $liveEvidence -and $restoreEvidence.StartsWith("25|")) "The restored backup did not preserve schema, funding, posted ledger, and audit evidence counts."

    $pendingRecharge = Invoke-SessionJSON -Method POST -URL "$controlURL/api/console/organizations/$organizationID/recharge-orders" -Session $consoleSession -CSRF $consoleCSRF -Body @{
        amount = "1.000000000000"; currency = "USD"; region = "CN"; payment_provider = "sandbox"; idempotency_key = "worker-recharge-$runID"
    }
    Assert-Status $pendingRecharge 201 "Creating the payment-worker restart fixture"
    $pendingRechargeID = [string]$pendingRecharge.JSON.order.id
    Assert-Status (Invoke-JSON -Method POST -URL "$mockURL/__test/scenario" -Headers @{ "X-RelayDock-Test-Token" = $mockTestToken } -Body @{ delay_ms = 10000; once = $true }) 200 "Configuring the ledger-worker restart fixture"
    $crashJob = Start-Job -ScriptBlock $gatewayJob -ArgumentList $gatewayURL, $apiKey, $routeAlias, "worker-crash-$runID", $false
    [void](Wait-PsqlValue -SQL "SELECT count(*) FROM funding_operation WHERE idempotency_key='worker-crash-$runID' AND status='RESERVED';" -Expected "1" -Operation "crash-recovery reservation")
    [void](Invoke-Docker -Arguments @("kill", $server) -Operation "Killing the active application instance")
    Start-Sleep -Seconds 4
    $failoverArguments = @($serverArguments)
    $serverNameIndex = [Array]::IndexOf($failoverArguments, $server)
    if ($serverNameIndex -lt 0) { throw "Could not construct the failover application arguments." }
    $failoverArguments[$serverNameIndex] = $failoverServer
    [void](Invoke-Docker -Arguments $failoverArguments -Operation "Starting the failover application instance")
    $containers.Add($failoverServer)
    $controlPort = Get-PublishedPort -Container $failoverServer -Port 8081
    $gatewayPort = Get-PublishedPort -Container $failoverServer -Port 8080
    $controlURL = "http://127.0.0.1:$controlPort"
    $gatewayURL = "http://127.0.0.1:$gatewayPort"
    Wait-HTTP -URL "$controlURL/readyz"
    $crashJob | Wait-Job -Timeout 10 | Out-Null
    $crashJob | Stop-Job -ErrorAction SilentlyContinue
    $crashJob | Remove-Job -Force -ErrorAction SilentlyContinue
    [void](Wait-PsqlValue -SQL "SELECT count(*) FROM funding_operation operation JOIN wallets wallet ON wallet.id=operation.wallet_id WHERE operation.idempotency_key='worker-crash-$runID' AND operation.status IN('SETTLED','PARTIALLY_SETTLED','FAILED','RELEASED') AND operation.usage_source='ESTIMATED_CRASH_RECOVERY' AND operation.settled_amount+operation.released_amount=operation.maximum_amount AND wallet.reserved_balance=0;" -Expected "1" -TimeoutSeconds 45 -Operation "ledger worker crash recovery")
    [void](Wait-PsqlValue -SQL "SELECT count(*) FROM recharge_order WHERE id='$pendingRechargeID' AND status='EXPIRED';" -Expected "1" -Operation "payment worker restart recovery")
    Assert-Status (Invoke-JSON -Method POST -URL "$gatewayURL/v1/responses" -Headers @{ Authorization = "Bearer $apiKey"; "Idempotency-Key" = "instance-failover-$runID" } -Body @{ model = $routeAlias; input = "Instance failover"; max_output_tokens = 16 }) 200 "Serving a request after application instance failure"

    $providerAttemptsBeforeDisable = [int](Invoke-Psql -SQL "SELECT count(*) FROM funding_provider_attempt WHERE provider_id='$providerID';")
    Assert-Status (Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/providers/$providerID/kill-switch" -Session $adminSession -CSRF $adminCSRF -Body @{ enabled = $true }) 200 "Disabling the Provider kill switch"
    Assert-Status (Invoke-JSON -Method POST -URL "$gatewayURL/v1/responses" -Headers @{ Authorization = "Bearer $apiKey"; "Idempotency-Key" = "provider-disabled-$runID" } -Body @{ model = $routeAlias; input = "Provider disabled"; max_output_tokens = 16 }) 403 "Rejecting routing to a disabled Provider"
    Assert-True ([int](Invoke-Psql -SQL "SELECT count(*) FROM funding_provider_attempt WHERE provider_id='$providerID';") -eq $providerAttemptsBeforeDisable) "A disabled Provider received a new attempt."
    Assert-Status (Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/providers/$providerID/kill-switch" -Session $adminSession -CSRF $adminCSRF -Body @{ enabled = $false }) 200 "Re-enabling the isolated Provider after the kill-switch check"

    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    do {
        $callMilestones = Invoke-Psql -SQL "SELECT count(*) FROM commercial_funnel_events WHERE user_id='$userID' AND event_type IN ('FIRST_API_CALL','SECOND_API_CALL');"
        if ([int]$callMilestones -eq 2) { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    Assert-True ([int]$callMilestones -eq 2) "First/second API call funnel milestones were not transactionally recorded."

    $financeUsage = Invoke-SessionJSON -Method GET -URL "$controlURL/api/console/organizations/$organizationID/finance/usage" -Session $consoleSession -CSRF $consoleCSRF
    Assert-Status $financeUsage 200 "Viewing request-level usage and charges"
    $usageRows = @($financeUsage.JSON.data)
    Assert-True ($usageRows.Count -ge 2) "The user could not see both request-level charge rows."
    foreach ($row in $usageRows | Select-Object -First 2) {
        Assert-True (-not [string]::IsNullOrWhiteSpace([string]$row.customer_charge) -and [string]$row.currency -eq "USD") "Usage charge evidence was not returned as an exact amount/currency."
    }

    $onboardingAfter = Invoke-SessionJSON -Method GET -URL "$controlURL/api/console/onboarding?organization_id=$organizationID&project_id=$projectID" -Session $consoleSession -CSRF $consoleCSRF
    Assert-Status $onboardingAfter 200 "Reading completed onboarding evidence"
    Assert-True ([string]$onboardingAfter.JSON.project_id -eq $projectID) "Completed onboarding evidence was not scoped to the requested project."
    $requiredIncomplete = @($onboardingAfter.JSON.steps | Where-Object { $_.required -and -not $_.completed })
    Assert-True ([bool]$onboardingAfter.JSON.complete -and $requiredIncomplete.Count -eq 0) "The server-derived onboarding chain did not complete."

    $summary = Invoke-SessionJSON -Method GET -URL "$controlURL/api/admin/funnel/summary" -Session $adminSession -CSRF $adminCSRF
    Assert-Status $summary 200 "Reading the aggregate funnel summary"
    foreach ($eventType in @("HOMEPAGE_VISITED", "REGISTERED", "EMAIL_VERIFIED", "API_KEY_CREATED", "FIRST_RECHARGE", "FIRST_API_CALL", "SECOND_API_CALL", "FIRST_SUBSCRIPTION")) {
        Assert-True ([int64]$summary.JSON.counts.$eventType -ge 1) "Funnel summary is missing $eventType."
    }
    $stages = @($summary.JSON.stages)
    Assert-True ($stages.Count -eq 8 -and $null -eq $stages[0].conversion_from_previous) "Funnel stages did not preserve the null first-stage conversion baseline."
    for ($index = 1; $index -lt $stages.Count; $index++) {
        $previousCount = [int64]$stages[$index - 1].count
        if ($previousCount -eq 0) {
            Assert-True ($null -eq $stages[$index].conversion_from_previous) "A funnel stage with a zero denominator returned a conversion value."
        } else {
            Assert-True ($null -ne $stages[$index].conversion_from_previous) "A funnel stage with a non-zero denominator omitted its conversion value."
        }
    }
    Assert-True ($summary.JSON.call_success_semantics -eq "HTTP_2XX_AND_SETTLEMENT_TERMINAL") "Funnel call success semantics were not disclosed."

    $migrationVersion = Invoke-Psql -SQL "SELECT name FROM schema_migrations WHERE version=19;"
    Assert-True ($migrationVersion -eq "public_commercial_onboarding") "Migration 19 is absent from the isolated ledger."
    $latestMigrationVersion = Invoke-Psql -SQL "SELECT max(version) FROM schema_migrations;"
    Assert-True ($latestMigrationVersion -eq "25") "The isolated commercial onboarding ledger is not current through migration 25."
    $evidenceTableCount = Invoke-Psql -SQL "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('public_commercial_terms','public_payment_fee_schedule','commercial_funnel_events','commercial_funnel_api_call_counter');"
    Assert-True ([int]$evidenceTableCount -eq 4) "The commercial onboarding evidence schema is incomplete."
    $auditCount = Invoke-Psql -SQL "SELECT count(*) FROM audit_logs WHERE action IN ('commercial.public_terms.publish','commercial.payment_fee.publish');"
    Assert-True ([int]$auditCount -eq 2) "Publishing public price/terms evidence was not transactionally audited exactly once per version."
    $homepageRows = Invoke-Psql -SQL "SELECT count(*) FROM commercial_funnel_events WHERE event_type='HOMEPAGE_VISITED' AND idempotency_key='$homepageKey' AND length(anonymous_id_hash)=32;"
    Assert-True ([int]$homepageRows -eq 1) "Homepage replay created duplicate events or omitted the HMAC digest."

    $publicPayload = [string]::Join("`n", @($config.Raw, $publicProviders.Raw, $publicModels.Raw, $publicPricing.Raw, $summary.Raw))
    foreach ($secret in @($apiKey, $mockAPIKey, $sandboxSecret, "synthetic-admin-password-2026", $userEmail, $anonymousID)) {
        Assert-True (-not $publicPayload.Contains($secret)) "A public/aggregate response exposed secret or subject data."
    }

    Assert-Status (Invoke-SessionJSON -Method PATCH -URL "$controlURL/api/admin/users/$userID/status" -Session $adminSession -CSRF $adminCSRF -Body @{ status = "SUSPENDED" }) 200 "Disabling the user"
    Assert-Status (Invoke-JSON -Method GET -URL "$gatewayURL/v1/models" -Headers @{ Authorization = "Bearer $apiKey" }) 401 "Invalidating the disabled user's API key"
    $auditBeforeDeletion = [int](Invoke-Psql -SQL "SELECT count(*) FROM audit_logs WHERE actor_id='$userID' OR resource_id='$userID';")
    $deleteJob = Invoke-SessionJSON -Method POST -URL "$controlURL/api/admin/privacy/USER/$userID/jobs" -Session $adminSession -CSRF $adminCSRF -Body @{ job_type = "DELETE"; idempotency_key = "delete-user-$runID" }
    Assert-Status $deleteJob 202 "Requesting user data deletion"
    $deleteJobID = [string]$deleteJob.JSON.job.id
    [void](Wait-PsqlValue -SQL "SELECT count(*) FROM data_lifecycle_jobs WHERE id='$deleteJobID' AND status='COMPLETED';" -Expected "1" -Operation "user data deletion")
    $deletionEvidence = Invoke-Psql -SQL "SELECT concat_ws('|',(SELECT count(*) FROM users WHERE id='$userID' AND email LIKE 'deleted+%@example.invalid' AND display_name='Deleted User' AND status='CLOSED'),(SELECT count(*) FROM audit_logs WHERE actor_id='$userID' OR resource_id='$userID'),(SELECT count(*) FROM audit_logs WHERE action='privacy.retention_cleanup')) ;"
    $deletionParts = $deletionEvidence.Split("|")
    Assert-True ($deletionParts[0] -eq "1" -and [int]$deletionParts[1] -ge $auditBeforeDeletion -and [int]$deletionParts[2] -ge 1) "Data deletion or retained audit/log cleanup evidence is incomplete."

    Write-Host "PASS public configuration, legal-review disclosure, and pre-purchase fail-closed pricing"
    Write-Host "PASS exact subscription, Token, payment fee, bonus, tax, refund, Provider, and region disclosure"
    Write-Host "PASS registration, email verification, explicit plan, signed recharge, rdk_test_* key, first and second /v1 calls"
    Write-Host "PASS server-derived onboarding, exact usage/charge visibility, funnel idempotency, privacy, audit, and migrations 19-25"
    Write-Host "PASS ordinary/SSE/fallback/client-abort flows, exact reserve-settle-release, concurrent wallet requests, and request replay"
    Write-Host "PASS Provider timeout/429/500, Redis/database interruption, in-flight price version switch, and zero-difference four-way reconciliation"
    Write-Host "PASS sandbox refund replay, PostgreSQL backup restore, application failover, ledger/payment worker recovery, and monthly statement"
    Write-Host "PASS Provider/user disable, cross-organization denial, retention cleanup, and audited data deletion"
} finally {
    Get-Job -ErrorAction SilentlyContinue | Where-Object { $_.Name -like "Job*" } | Stop-Job -ErrorAction SilentlyContinue
    Get-Job -ErrorAction SilentlyContinue | Where-Object { $_.Name -like "Job*" } | Remove-Job -Force -ErrorAction SilentlyContinue
    $containerNames = @($containers)
    [Array]::Reverse($containerNames)
    foreach ($name in $containerNames) {
        if ($name -notmatch "^modeldock-onboarding-(pg|redis|mock|app|app2)-$runID$") {
            Write-Warning "Refused to remove a container whose generated name failed the ownership check."
            continue
        }
        & docker rm --force --volumes $name *> $null
    }
    if ($network -match "^modeldock-onboarding-$runID$") {
        & docker network rm $network *> $null
    }
    if ($ownsServerImage) { & docker image rm $serverImage *> $null }
    & docker image rm $mockImage *> $null
}
