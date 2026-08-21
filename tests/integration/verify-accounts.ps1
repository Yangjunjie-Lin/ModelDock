[CmdletBinding()]
param(
    [switch]$ConfirmIsolatedTestDatabase,
    [ValidateRange(30, 180)]
    [int]$StartupTimeoutSeconds = 90,
    [string]$ExistingImage = ""
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

if (-not $ConfirmIsolatedTestDatabase) {
    throw "Pass -ConfirmIsolatedTestDatabase only for a disposable local Docker run."
}
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$runID = [Guid]::NewGuid().ToString("N").Substring(0, 16)
$network = "modeldock-accounts-$runID"
$postgres = "modeldock-accounts-pg-$runID"
$redis = "modeldock-accounts-redis-$runID"
$server = "modeldock-accounts-app-$runID"
$closedServer = "modeldock-accounts-closed-$runID"
$inviteServer = "modeldock-accounts-invite-$runID"
$image = if ($ExistingImage) { $ExistingImage } else { "modeldock/accounts-integration:$runID" }
$ownsImage = -not [bool]$ExistingImage
$containers = [Collections.Generic.List[string]]::new()

function Invoke-Docker {
    param([string[]]$Arguments, [string]$Operation)
    $output = @(& docker @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "$Operation failed; Docker diagnostic output was suppressed."
    }
    return @($output | ForEach-Object { [string]$_ })
}

function Wait-ContainerCommand {
    param([string]$Container, [string[]]$Command, [string]$Operation)
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    do {
        & docker exec $Container @Command *> $null
        if ($LASTEXITCODE -eq 0) { return }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$Operation did not become ready before the timeout."
}

function Get-PublishedPort {
    param([string]$Container, [int]$Port)
    $line = (Invoke-Docker -Arguments @("port", $Container, "$Port/tcp") -Operation "Reading published port" | Select-Object -First 1)
    if ($line -notmatch ':(\d+)$') { throw "Docker returned an invalid published port." }
    return [int]$Matches[1]
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
    $arguments = @{ UseBasicParsing = $true; Uri = $URL; Method = $Method; TimeoutSec = 10; Headers = $Headers }
    if ($null -ne $Session) { $arguments.WebSession = $Session }
    if ($null -ne $Body) {
        $arguments.ContentType = "application/json"
        $arguments.Body = ConvertTo-Json $Body -Depth 8 -Compress
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
        } elseif ($null -ne $errorResponse.Content) {
            $content = $errorResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        }
    }
    $json = $null
    if (-not [string]::IsNullOrWhiteSpace($content)) { $json = $content | ConvertFrom-Json }
    return [pscustomobject]@{ Status = $status; JSON = $json; Raw = $content }
}

function Assert-Status {
    param($Response, [int]$Expected, [string]$Operation)
    if ($Response.Status -ne $Expected) {
        $detail = if ([string]::IsNullOrWhiteSpace([string]$Response.Raw)) { "" } else { " Response: $($Response.Raw)" }
        throw "$Operation returned HTTP $($Response.Status), expected $Expected.$detail"
    }
}

function Get-CSRFHeader {
    param([Microsoft.PowerShell.Commands.WebRequestSession]$Session, [string]$BaseURL, [string]$CookieName)
    $cookie = $Session.Cookies.GetCookies([Uri]$BaseURL)[$CookieName]
    if ($null -eq $cookie -or [string]::IsNullOrWhiteSpace($cookie.Value)) { throw "Expected CSRF cookie is missing." }
    return @{ "X-CSRF-Token" = $cookie.Value }
}

function Get-MailFiles {
    param([string]$Container)
    $result = @(& docker exec $Container sh -c 'find /tmp/mail -maxdepth 1 -type f -name "*.json" -print 2>/dev/null' 2>$null)
    if ($LASTEXITCODE -ne 0) { return @() }
    return @($result | ForEach-Object { [string]$_ })
}

function Wait-NewMailToken {
    param([string]$Container, [string[]]$Before, [ValidateSet("query", "invitation")][string]$Kind)
    $known = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($item in $Before) { [void]$known.Add($item) }
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    do {
        foreach ($file in (Get-MailFiles -Container $Container)) {
            if ($known.Contains($file)) { continue }
            $content = [string]::Join("`n", @(& docker exec $Container sh -c "cat '$file'" 2>$null))
            if ($LASTEXITCODE -ne 0) { continue }
            $message = $content | ConvertFrom-Json
            if ($Kind -eq "query" -and [string]$message.text -match '[?&]token=([^&\s]+)') { return [Uri]::UnescapeDataString($Matches[1]) }
            if ($Kind -eq "invitation" -and [string]$message.text -match '/invitations/([^?\s]+)') { return [Uri]::UnescapeDataString($Matches[1]) }
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Expected captured email did not arrive."
}

function ConvertFrom-Base32 {
    param([string]$Value)
    $alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
    $buffer = 0
    $bits = 0
    $bytes = [Collections.Generic.List[byte]]::new()
    foreach ($character in $Value.Trim().ToUpperInvariant().ToCharArray()) {
        $index = $alphabet.IndexOf($character)
        if ($index -lt 0) { throw "Invalid Base32 value." }
        $buffer = ($buffer -shl 5) -bor $index
        $bits += 5
        if ($bits -ge 8) {
            $bits -= 8
            $bytes.Add([byte](($buffer -shr $bits) -band 0xff))
            $buffer = $buffer -band ((1 -shl $bits) - 1)
        }
    }
    return $bytes.ToArray()
}

function Get-TOTP {
    param([string]$Secret)
    $counter = [Int64][Math]::Floor([DateTimeOffset]::UtcNow.ToUnixTimeSeconds() / 30)
    $counterBytes = [BitConverter]::GetBytes($counter)
    if ([BitConverter]::IsLittleEndian) { [Array]::Reverse($counterBytes) }
    $hmac = [Security.Cryptography.HMACSHA1]::new((ConvertFrom-Base32 $Secret))
    try { $hash = $hmac.ComputeHash($counterBytes) } finally { $hmac.Dispose() }
    $offset = $hash[$hash.Length - 1] -band 0x0f
    [uint32]$number = ([uint32]($hash[$offset] -band 0x7f) -shl 24) -bor ([uint32]$hash[$offset + 1] -shl 16) -bor ([uint32]$hash[$offset + 2] -shl 8) -bor [uint32]$hash[$offset + 3]
    return ($number % 1000000).ToString("D6")
}

function Start-AppContainer {
    param([string]$Name, [string]$Mode)
    $arguments = @(
        "run", "-d", "--name", $Name, "--network", $network,
        "-p", "127.0.0.1::8080", "-p", "127.0.0.1::8081",
        "-e", "DATABASE_URL=postgres://relayedock:synthetic-db-password@$postgres`:5432/relayedock?sslmode=disable",
        "-e", "REDIS_URL=redis://$redis`:6379/0",
        "-e", "RELAYDOCK_MASTER_KEY=0123456789abcdef0123456789abcdef",
        "-e", "RELAYDOCK_API_KEY_HMAC_SECRET=abcdef0123456789abcdef0123456789",
        "-e", "RELAYDOCK_JWT_SECRET=89abcdef0123456789abcdef01234567",
        "-e", "RELAYDOCK_ADMIN_EMAIL=admin@example.invalid",
        "-e", "RELAYDOCK_ADMIN_PASSWORD=synthetic-admin-password-2026",
        "-e", "RELAYDOCK_REGISTRATION_MODE=$Mode",
        "-e", "RELAYDOCK_ADMIN_MFA_REQUIRED=true",
        "-e", "RELAYDOCK_MAIL_PROVIDER=local",
        "-e", "RELAYDOCK_MAIL_CAPTURE_DIR=/tmp/mail",
        "-e", "RELAYDOCK_PUBLIC_CONSOLE_URL=http://console.example.invalid",
        "-e", "COOKIE_SECURE=false", $image
    )
    [void](Invoke-Docker -Arguments $arguments -Operation "Starting $Mode application")
    $containers.Add($Name)
    $controlPort = Get-PublishedPort -Container $Name -Port 8081
    $gatewayPort = Get-PublishedPort -Container $Name -Port 8080
    $controlURL = "http://127.0.0.1:$controlPort"
    try {
        Wait-HTTP -URL "$controlURL/readyz"
    } catch {
        $logText = [string]::Join("`n", @(& docker logs --tail 30 $Name 2>&1))
        $logText = $logText -replace 'postgres://[^\s"]+', 'postgres://[redacted]' -replace 'redis://[^\s"]+', 'redis://[redacted]' -replace 'rdk_(live|test)_[A-Za-z0-9_-]+', 'rdk_$1_[redacted]'
        throw "Application did not become ready. Sanitized logs:`n$logText"
    }
    return [pscustomobject]@{ Control = $controlURL; Gateway = "http://127.0.0.1:$gatewayPort" }
}

try {
    if ($ownsImage) {
        [void](Invoke-Docker -Arguments @("build", "--quiet", "-f", (Join-Path $repoRoot "deploy/docker/Dockerfile.relaydock"), "-t", $image, $repoRoot) -Operation "Building account integration image")
    } else {
        [void](Invoke-Docker -Arguments @("image", "inspect", $image) -Operation "Inspecting the prebuilt account integration image")
    }
    [void](Invoke-Docker -Arguments @("network", "create", $network) -Operation "Creating isolated network")
    [void](Invoke-Docker -Arguments @("run", "-d", "--name", $postgres, "--network", $network, "-e", "POSTGRES_DB=relayedock", "-e", "POSTGRES_USER=relayedock", "-e", "POSTGRES_PASSWORD=synthetic-db-password", "postgres:17-alpine") -Operation "Starting isolated PostgreSQL")
    $containers.Add($postgres)
    [void](Invoke-Docker -Arguments @("run", "-d", "--name", $redis, "--network", $network, "redis:7.4-alpine") -Operation "Starting isolated Redis")
    $containers.Add($redis)
    Wait-ContainerCommand -Container $postgres -Command @("pg_isready", "-U", "relayedock", "-d", "relayedock") -Operation "PostgreSQL"
    Wait-ContainerCommand -Container $redis -Command @("redis-cli", "ping") -Operation "Redis"

    $public = Start-AppContainer -Name $server -Mode "PUBLIC"
    $config = Invoke-JSON -Method GET -URL "$($public.Control)/api/console/auth/config"
    Assert-Status $config 200 "Reading registration config"
    if ($config.JSON.registration_mode -ne "PUBLIC") { throw "Registration mode was not PUBLIC." }

    $userEmail = "user-$runID@example.invalid"
    $initialPassword = "synthetic-user-password-2026"
    $mailBefore = Get-MailFiles -Container $server
    $registration = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/register" -Body @{ email = $userEmail; password = $initialPassword; display_name = "Synthetic User" }
    Assert-Status $registration 202 "Registering user"
    $verificationToken = Wait-NewMailToken -Container $server -Before $mailBefore -Kind query
    $verification = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/verify-email" -Body @{ token = $verificationToken }
    Assert-Status $verification 200 "Verifying email"
    $userID = [string]$verification.JSON.user.id
    $verificationReplay = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/verify-email" -Body @{ token = $verificationToken }
    Assert-Status $verificationReplay 400 "Replaying verification token"

    $consoleSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $login = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Body @{ email = $userEmail; password = $initialPassword } -Session $consoleSession
    Assert-Status $login 200 "Console login"
    $consoleCSRF = Get-CSRFHeader -Session $consoleSession -BaseURL $public.Control -CookieName "relayedock_console_csrf"
    $projects = Invoke-JSON -Method GET -URL "$($public.Control)/api/console/projects" -Session $consoleSession
    Assert-Status $projects 200 "Listing first workspace"
    $projectRows = @($projects.JSON.data)
    if ($projectRows.Count -lt 1) { throw "Verified user did not receive a project." }
    $keyResult = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/api-keys" -Session $consoleSession -Headers $consoleCSRF -Body @{ name = "integration"; environment = "test"; project_id = [string]$projectRows[0].id }
    Assert-Status $keyResult 201 "Creating first API key"
    $apiKey = [string]$keyResult.JSON.key
    if (-not $apiKey.StartsWith("rdk_test_")) { throw "Created key did not preserve the rdk_test_ format." }
    $models = Invoke-JSON -Method GET -URL "$($public.Gateway)/v1/models" -Headers @{ Authorization = "Bearer $apiKey" }
    Assert-Status $models 200 "Calling the first OpenAI-compatible API"
    $replacement = if ($apiKey.EndsWith("A")) { "B" } else { "A" }
    $unknownAPIKey = $apiKey.Substring(0, $apiKey.Length - 1) + $replacement
    $unknownAPIKeyResponse = Invoke-JSON -Method GET -URL "$($public.Gateway)/v1/models" -Headers @{ Authorization = "Bearer $unknownAPIKey" }
    $invalidAPIKeyResponse = Invoke-JSON -Method GET -URL "$($public.Gateway)/v1/models" -Headers @{ Authorization = "Bearer rdk_test_invalid" }
    if ($unknownAPIKeyResponse.Status -ne 401 -or $invalidAPIKeyResponse.Status -ne 401 -or $unknownAPIKeyResponse.Raw -ne $invalidAPIKeyResponse.Raw) {
        throw "API key authentication responses permit key enumeration."
    }

    $knownWrong = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Body @{ email = $userEmail; password = "synthetic-wrong-password" }
    $unknownWrong = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Body @{ email = "unknown-$runID@example.invalid"; password = "synthetic-wrong-password" }
    if ($knownWrong.Status -ne 401 -or $unknownWrong.Status -ne 401 -or $knownWrong.Raw -ne $unknownWrong.Raw) { throw "Login responses permit account enumeration." }

    $resetBefore = Get-MailFiles -Container $server
    $forgotKnown = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/forgot-password" -Body @{ email = $userEmail }
    $forgotUnknown = Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/forgot-password" -Body @{ email = "unknown-$runID@example.invalid" }
    if ($forgotKnown.Status -ne 202 -or $forgotUnknown.Status -ne 202 -or $forgotKnown.Raw -ne $forgotUnknown.Raw) { throw "Password reset responses permit account enumeration." }
    $resetToken = Wait-NewMailToken -Container $server -Before $resetBefore -Kind query
    $newPassword = "synthetic-user-password-reset-2026"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/reset-password" -Body @{ token = $resetToken; password = $newPassword }) 200 "Resetting password"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/reset-password" -Body @{ token = $resetToken; password = $newPassword }) 400 "Replaying reset token"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/console/projects" -Session $consoleSession) 401 "Using a session revoked by reset"

    $adminSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $adminLogin = Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/auth/login" -Session $adminSession -Body @{ email = "admin@example.invalid"; password = "synthetic-admin-password-2026" }
    Assert-Status $adminLogin 200 "Initial administrator login"
    if (-not $adminLogin.JSON.mfa_enrollment_required) { throw "Administrator login did not require MFA enrollment." }
    $adminCSRF = Get-CSRFHeader -Session $adminSession -BaseURL $public.Control -CookieName "relayedock_admin_csrf"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/admin/dashboard" -Session $adminSession) 403 "Accessing admin API before MFA"
    $mfaSetup = Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/auth/mfa/setup" -Session $adminSession -Headers $adminCSRF
    Assert-Status $mfaSetup 200 "Starting MFA enrollment"
    $mfaCode = Get-TOTP -Secret ([string]$mfaSetup.JSON.secret)
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/auth/mfa/confirm" -Session $adminSession -Headers $adminCSRF -Body @{ code = $mfaCode }) 200 "Confirming MFA enrollment"
    $adminCSRF = Get-CSRFHeader -Session $adminSession -BaseURL $public.Control -CookieName "relayedock_admin_csrf"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/admin/dashboard" -Session $adminSession) 200 "Accessing admin API after MFA"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/auth/login" -Body @{ email = "admin@example.invalid"; password = "synthetic-admin-password-2026" }) 401 "Logging in without required TOTP"
    $usedStep = [Math]::Floor([DateTimeOffset]::UtcNow.ToUnixTimeSeconds() / 30)
    while ([Math]::Floor([DateTimeOffset]::UtcNow.ToUnixTimeSeconds() / 30) -le $usedStep) { Start-Sleep -Milliseconds 250 }
    $freshCode = Get-TOTP -Secret ([string]$mfaSetup.JSON.secret)
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/auth/login" -Body @{ email = "admin@example.invalid"; password = "synthetic-admin-password-2026"; mfa_code = $freshCode }) 200 "Logging in with TOTP"

    $organization = Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/organizations" -Session $adminSession -Headers $adminCSRF -Body @{ name = "Integration Organization"; slug = "integration-$runID"; status = "ACTIVE"; metadata = @{} }
    Assert-Status $organization 201 "Creating organization"
    $inviteMailBefore = Get-MailFiles -Container $server
    $invitation = Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/organizations/$($organization.JSON.id)/invitations" -Session $adminSession -Headers $adminCSRF -Body @{ email = "closed-$runID@example.invalid"; role = "MEMBER" }
    Assert-Status $invitation 201 "Creating organization invitation"
    $invitationToken = Wait-NewMailToken -Container $server -Before $inviteMailBefore -Kind invitation

    $closed = Start-AppContainer -Name $closedServer -Mode "CLOSED"
    Assert-Status (Invoke-JSON -Method POST -URL "$($closed.Control)/api/console/auth/register" -Body @{ email = "bypass-$runID@example.invalid"; password = "synthetic-bypass-password"; display_name = "Bypass" }) 403 "Registering while CLOSED"
    Assert-Status (Invoke-JSON -Method POST -URL "$($closed.Control)/api/console/auth/invitations/$invitationToken/accept" -Body @{ password = "synthetic-invited-password"; display_name = "Invited" }) 403 "Creating an invited account while CLOSED"
    Assert-Status (Invoke-JSON -Method DELETE -URL "$($public.Control)/api/admin/organizations/$($organization.JSON.id)/invitations/$($invitation.JSON.id)" -Session $adminSession -Headers $adminCSRF) 204 "Revoking organization invitation"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/console/auth/invitations/$invitationToken") 404 "Previewing revoked invitation"
    [void](Invoke-Docker -Arguments @("rm", "-f", $closedServer) -Operation "Stopping CLOSED test application")
    [void]$containers.Remove($closedServer)

    $rejectMailBefore = Get-MailFiles -Container $server
    $rejectInvitation = Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/organizations/$($organization.JSON.id)/invitations" -Session $adminSession -Headers $adminCSRF -Body @{ email = "reject-$runID@example.invalid"; role = "VIEWER" }
    Assert-Status $rejectInvitation 201 "Creating rejectable invitation"
    $rejectToken = Wait-NewMailToken -Container $server -Before $rejectMailBefore -Kind invitation
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/invitations/$rejectToken/reject") 200 "Rejecting organization invitation"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/invitations/$rejectToken/reject") 400 "Replaying rejected invitation"

    $acceptMailBefore = Get-MailFiles -Container $server
    $acceptEmail = "accept-$runID@example.invalid"
    $acceptPassword = "synthetic-accepted-password"
    $acceptInvitation = Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/organizations/$($organization.JSON.id)/invitations" -Session $adminSession -Headers $adminCSRF -Body @{ email = $acceptEmail; role = "MEMBER" }
    Assert-Status $acceptInvitation 201 "Creating acceptable invitation"
    $acceptToken = Wait-NewMailToken -Container $server -Before $acceptMailBefore -Kind invitation
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/invitations/$acceptToken/accept" -Body @{ password = $acceptPassword; display_name = "Accepted User" }) 200 "Accepting organization invitation"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/invitations/$acceptToken/accept" -Body @{ password = $acceptPassword; display_name = "Accepted User" }) 400 "Replaying accepted invitation"

    $acceptedSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Session $acceptedSession -Body @{ email = $acceptEmail; password = $acceptPassword }) 200 "Logging in to accepted organization"
    $isolationSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Session $isolationSession -Body @{ email = $userEmail; password = $newPassword }) 200 "Starting organization isolation session"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/console/organizations/$($organization.JSON.id)" -Session $acceptedSession) 200 "Reading invited organization"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/console/organizations/$($organization.JSON.id)" -Session $isolationSession) 404 "Reading another user's organization"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/console/projects/$([string]$projectRows[0].id)" -Session $acceptedSession) 404 "Reading another organization's project"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/admin/dashboard" -Session $isolationSession) 401 "Using a console session as an administrator"

    $registrationInvite = Invoke-JSON -Method POST -URL "$($public.Control)/api/admin/registration-invites" -Session $adminSession -Headers $adminCSRF -Body @{ max_uses = 1; expires_in_hours = 1 }
    Assert-Status $registrationInvite 201 "Creating registration invite"
    $inviteOnly = Start-AppContainer -Name $inviteServer -Mode "INVITE_ONLY"
    Assert-Status (Invoke-JSON -Method POST -URL "$($inviteOnly.Control)/api/console/auth/register" -Body @{ email = "no-code-$runID@example.invalid"; password = "synthetic-invite-password"; display_name = "No Code" }) 400 "Registering without code in INVITE_ONLY mode"
    Assert-Status (Invoke-JSON -Method POST -URL "$($inviteOnly.Control)/api/console/auth/register" -Body @{ email = "with-code-$runID@example.invalid"; password = "synthetic-invite-password"; display_name = "With Code"; registration_code = [string]$registrationInvite.JSON.code }) 202 "Registering with code in INVITE_ONLY mode"
    Assert-Status (Invoke-JSON -Method POST -URL "$($inviteOnly.Control)/api/console/auth/register" -Body @{ email = "reused-code-$runID@example.invalid"; password = "synthetic-invite-password"; display_name = "Reused Code"; registration_code = [string]$registrationInvite.JSON.code }) 400 "Reusing exhausted registration code"
    [void](Invoke-Docker -Arguments @("rm", "-f", $inviteServer) -Operation "Stopping INVITE_ONLY test application")
    [void]$containers.Remove($inviteServer)

    Assert-Status (Invoke-JSON -Method PATCH -URL "$($public.Control)/api/admin/users/$userID/status" -Session $adminSession -Headers $adminCSRF -Body @{ status = "SUSPENDED" }) 200 "Suspending account"
    $suspendedSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Session $suspendedSession -Body @{ email = $userEmail; password = $newPassword }) 401 "Logging in to suspended account"
    Assert-Status (Invoke-JSON -Method PATCH -URL "$($public.Control)/api/admin/users/$userID/status" -Session $adminSession -Headers $adminCSRF -Body @{ status = "ACTIVE" }) 200 "Restoring account"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Body @{ email = $userEmail; password = $newPassword }) 200 "Logging in to restored account"

    $changeSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Session $changeSession -Body @{ email = $userEmail; password = $newPassword }) 200 "Starting password-change session"
    $changeCSRF = Get-CSRFHeader -Session $changeSession -BaseURL $public.Control -CookieName "relayedock_console_csrf"
    $changedPassword = "synthetic-user-password-changed-2026"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/change-password" -Session $changeSession -Headers $changeCSRF -Body @{ current_password = $newPassword; new_password = $changedPassword }) 200 "Changing password"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/console/projects" -Session $changeSession) 401 "Using session cleared by password change"

    $keeperSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $otherSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Session $keeperSession -Body @{ email = $userEmail; password = $changedPassword }) 200 "Starting keeper session"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/login" -Session $otherSession -Body @{ email = $userEmail; password = $changedPassword }) 200 "Starting other session"
    $keeperCSRF = Get-CSRFHeader -Session $keeperSession -BaseURL $public.Control -CookieName "relayedock_console_csrf"
    Assert-Status (Invoke-JSON -Method POST -URL "$($public.Control)/api/console/auth/logout-other-sessions" -Session $keeperSession -Headers $keeperCSRF) 200 "Revoking other sessions"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/console/projects" -Session $otherSession) 401 "Using revoked other session"
    Assert-Status (Invoke-JSON -Method GET -URL "$($public.Control)/api/console/projects" -Session $keeperSession) 200 "Using retained current session"

    Write-Output "Account integration verification passed."
} finally {
    for ($index = $containers.Count - 1; $index -ge 0; $index--) {
        $name = $containers[$index]
        if ($name -match "^modeldock-accounts-(pg|redis|app|closed|invite)-$runID$") { & docker rm -f $name *> $null }
    }
    if ($network -match "^modeldock-accounts-$runID$") { & docker network rm $network *> $null }
    if ($ownsImage) { & docker image rm $image *> $null }
}
