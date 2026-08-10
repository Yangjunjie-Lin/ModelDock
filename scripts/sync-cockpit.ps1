[CmdletBinding()]
param(
    [string]$CockpitRoot = "$env:USERPROFILE\.antigravity_cockpit",
    [string]$OutputPath = (Join-Path $PSScriptRoot "..\data\cockpit\accounts.json"),
    [switch]$Test,
    [string]$TestModel = "gpt-5.6-luna"
)

$ErrorActionPreference = "Stop"

function ConvertFrom-UnixSeconds {
    param([object]$Value)
    if ($null -eq $Value -or [long]$Value -le 0) { return $null }
    return [DateTimeOffset]::FromUnixTimeSeconds([long]$Value).UtcDateTime.ToString("o")
}

function ConvertFrom-UnixMilliseconds {
    param([object]$Value)
    if ($null -eq $Value -or [long]$Value -le 0) { return $null }
    return [DateTimeOffset]::FromUnixTimeMilliseconds([long]$Value).UtcDateTime.ToString("o")
}

function Protect-Email {
    param([string]$Email)
    if ([string]::IsNullOrWhiteSpace($Email) -or -not $Email.Contains("@")) { return "authorized-account" }
    $parts = $Email.Split("@", 2)
    $prefix = if ($parts[0].Length -gt 0) { $parts[0].Substring(0, 1) } else { "*" }
    return "$prefix***@$($parts[1])"
}

function Get-SafeAccountID {
    param([string]$Value)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($Value)
        $hash = [BitConverter]::ToString($sha.ComputeHash($bytes)).Replace("-", "").ToLowerInvariant()
        return "acct-$($hash.Substring(0, 12))"
    }
    finally {
        $sha.Dispose()
    }
}

$manifestPath = Join-Path $CockpitRoot "codex_local_access_sidecar\manifest.json"
$quotaPath = Join-Path $CockpitRoot "codex_local_access_sidecar\quota-pool-state.json"
$configPath = Join-Path $CockpitRoot "codex_local_access.json"
if (-not (Test-Path -LiteralPath $manifestPath) -or -not (Test-Path -LiteralPath $quotaPath)) {
    throw "Cockpit sidecar manifest or quota state was not found under $CockpitRoot"
}

$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
$quota = Get-Content -LiteralPath $quotaPath -Raw | ConvertFrom-Json
$accounts = @()
foreach ($account in @($manifest.accounts)) {
    $quotaProperty = $quota.accounts.PSObject.Properties | Where-Object { $_.Name -eq [string]$account.id } | Select-Object -First 1
    $quotaValue = if ($null -ne $quotaProperty) { $quotaProperty.Value } else { $null }
    $remainingPercent = if ($null -ne $quotaValue -and $null -ne $quotaValue.primary.remainingPercent) { [int]$quotaValue.primary.remainingPercent } else { [int]$account.remainingQuota }
    $secondaryPercent = if ($null -ne $quotaValue -and $null -ne $quotaValue.secondary.remainingPercent) { [int]$quotaValue.secondary.remainingPercent } else { 0 }
    $status = if ($remainingPercent -gt 0) { "ready" } else { "quota_exhausted" }
    $accounts += [ordered]@{
        id                      = Get-SafeAccountID ([string]$account.id)
        email_masked            = Protect-Email ([string]$account.email)
        plan                    = ([string]$account.planType).ToLowerInvariant()
        auth_kind               = ([string]$account.authKind).ToLowerInvariant()
        status                  = $status
        remaining_quota         = [int]$account.remainingQuota
        remaining_percent       = $remainingPercent
        secondary_percent       = $secondaryPercent
        reset_at                = if ($null -ne $quotaValue) { ConvertFrom-UnixSeconds $quotaValue.primary.resetAt } else { $null }
        subscription_expires_at = ConvertFrom-UnixMilliseconds $account.subscriptionExpiryMs
        updated_at              = if ($null -ne $quotaValue) { ConvertFrom-UnixSeconds $quotaValue.updatedAt } else { $null }
    }
}

$lastTest = $null
if ($Test) {
    if (-not (Test-Path -LiteralPath $configPath)) { throw "Cockpit local access configuration was not found." }
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $headers = @{ Authorization = "Bearer $([string]$config.apiKey)"; "Content-Type" = "application/json" }
    $baseURL = "http://127.0.0.1:$([int]$config.port)"
    $body = @{ model = $TestModel; input = "Reply exactly: RELAYDOCK_COCKPIT_OK"; max_output_tokens = 32 } | ConvertTo-Json -Compress
    $started = [Diagnostics.Stopwatch]::StartNew()
    $response = Invoke-RestMethod -Uri "$baseURL/v1/responses" -Headers $headers -Method Post -Body $body -TimeoutSec 90
    $started.Stop()
    $output = [string]$response.output_text
    if ([string]::IsNullOrWhiteSpace($output)) {
        $output = (@($response.output) | ForEach-Object { @($_.content) | ForEach-Object { if ($_.text) { [string]$_.text } } }) -join ""
    }
    $ok = $output.Trim() -eq "RELAYDOCK_COCKPIT_OK"
    $lastTest = [ordered]@{
        ok         = $ok
        model      = $TestModel
        latency_ms = [long]$started.ElapsedMilliseconds
        tested_at  = [DateTime]::UtcNow.ToString("o")
        message    = if ($ok) { "Cockpit sidecar model check passed" } else { "Cockpit sidecar verification text did not match" }
    }
    if (-not $ok) { throw "Cockpit returned a response, but the verification text did not match." }
}

$snapshot = [ordered]@{
    source       = "cockpit-local-sidecar"
    generated_at = [DateTime]::UtcNow.ToString("o")
    accounts     = $accounts
    last_test    = $lastTest
}
$fullOutputPath = [IO.Path]::GetFullPath($OutputPath)
$outputDirectory = Split-Path -Parent $fullOutputPath
[IO.Directory]::CreateDirectory($outputDirectory) | Out-Null
$json = $snapshot | ConvertTo-Json -Depth 8
[IO.File]::WriteAllText($fullOutputPath, $json, [Text.UTF8Encoding]::new($false))
Write-Output "Wrote sanitized Cockpit snapshot with $($accounts.Count) account(s); live test: $([bool]$Test)."
