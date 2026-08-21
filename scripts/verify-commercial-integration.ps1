[CmdletBinding()]
param(
    [string]$EnvFile = ".env",
    [switch]$ConfirmIsolatedTestDatabase,
    [string]$OutputDirectory = ".cache/commercial-results"
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
if (-not $ConfirmIsolatedTestDatabase) {
    throw "Pass -ConfirmIsolatedTestDatabase only for disposable CI/local test infrastructure."
}
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
if (-not [System.IO.Path]::IsPathRooted($EnvFile)) { $EnvFile = Join-Path $repoRoot $EnvFile }
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) { throw "Commercial integration environment file is missing." }
$output = if ([System.IO.Path]::IsPathRooted($OutputDirectory)) {
    [System.IO.Path]::GetFullPath($OutputDirectory)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDirectory))
}
if (-not $output.StartsWith($repoRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Commercial integration output must remain inside the repository."
}
[System.IO.Directory]::CreateDirectory($output) | Out-Null
$commit = (git -C $repoRoot rev-parse HEAD).Trim()
$latestMigration = (Get-ChildItem -LiteralPath (Join-Path $repoRoot "migrations") -File -Filter "*.sql" | ForEach-Object {
    if ($_.BaseName -match '^(\d{4})_') { [int]$Matches[1] }
} | Measure-Object -Maximum).Maximum
$started = [DateTimeOffset]::UtcNow
$status = "FAIL"
$completed = [System.Collections.Generic.List[string]]::new()

function Protect-CommercialLog([string]$Text) {
    $safe = [regex]::Replace($Text, '(?i)\b(postgres(?:ql)?|redis)://\S+', '$1://[REDACTED]')
    $safe = [regex]::Replace($safe, '(?i)\bBearer\s+\S+', 'Bearer [REDACTED]')
    $safe = [regex]::Replace($safe, '\brdk_(live|test)_[A-Za-z0-9_-]+', 'rdk_$1_[REDACTED]')
    return [regex]::Replace($safe, '(?i)\b(password|secret|token|api[_-]?key)\s*[=:]\s*\S+', '$1=[REDACTED]')
}

function Invoke-Suite([string]$Name, [scriptblock]$Action) {
    $log = Join-Path $output "$Name.log"
    [System.IO.File]::WriteAllText($log, "", (New-Object System.Text.UTF8Encoding($false)))
    try {
        $global:LASTEXITCODE = 0
        & $Action *>&1 | ForEach-Object {
            $safe = Protect-CommercialLog ([string]$_)
            [System.IO.File]::AppendAllText($log, $safe + [Environment]::NewLine, (New-Object System.Text.UTF8Encoding($false)))
            Write-Host $safe
        }
        if ($LASTEXITCODE -ne 0) { throw "Suite returned native exit code $LASTEXITCODE." }
        $script:completed.Add($Name)
    } catch {
        $safeError = Protect-CommercialLog ($_ | Out-String)
        [System.IO.File]::AppendAllText($log, $safeError + [Environment]::NewLine, (New-Object System.Text.UTF8Encoding($false)))
        throw "Commercial integration suite '$Name' failed. See its sanitized artifact log."
    }
}

try {
    Invoke-Suite "verify-migrations" { & (Join-Path $repoRoot "tests/integration/verify-migrations.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-accounts" { & (Join-Path $repoRoot "tests/integration/verify-accounts.ps1") -ConfirmIsolatedTestDatabase -ExistingImage relaydock/server:local }
    Invoke-Suite "verify-pricing" { & (Join-Path $repoRoot "tests/integration/verify-pricing.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-funding" { & (Join-Path $repoRoot "tests/integration/verify-funding.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-payments" { & (Join-Path $repoRoot "tests/integration/verify-payments.ps1") -EnvFile $EnvFile }
    Invoke-Suite "verify-subscriptions" { & (Join-Path $repoRoot "tests/integration/verify-subscriptions.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-financial-close" { & (Join-Path $repoRoot "tests/integration/verify-financial-close.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-commercial-onboarding" { & (Join-Path $repoRoot "tests/integration/verify-commercial-onboarding.ps1") -ConfirmIsolatedTestDatabase -StartupTimeoutSeconds 180 -ExistingServerImage relaydock/server:local }
    Invoke-Suite "verify-supplier-settlement" { & (Join-Path $repoRoot "tests/integration/verify-supplier-settlement.ps1") -EnvFile $EnvFile -GoExecutable go -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-marketplace-launch" { & (Join-Path $repoRoot "tests/integration/verify-marketplace-launch.ps1") -EnvFile $EnvFile -GoExecutable go -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-release-metadata" { & (Join-Path $repoRoot "tests/integration/verify-release-metadata.ps1") -Version (Get-Content -LiteralPath (Join-Path $repoRoot "VERSION") -Raw).Trim() -Commit $commit }
    Invoke-Suite "verify-exact-money" { & (Join-Path $repoRoot "scripts/verify-exact-money.ps1") }
    Invoke-Suite "verify-release-gates" { & (Join-Path $repoRoot "tests/release/verify-commercial-readiness.ps1") }
    Invoke-Suite "verify-engineering-readiness" { & (Join-Path $repoRoot "scripts/verify-commercial-readiness.ps1") -Profile ENGINEERING_PREVIEW -Commit $commit -ReportOutput (Join-Path $output "engineering-readiness.md") }
    $status = "PASS"
} finally {
    $imageDigest = ""
    $digestOutput = docker image inspect relaydock/server:local --format '{{.Id}}' 2>$null
    if ($LASTEXITCODE -eq 0) { $imageDigest = ([string]$digestOutput).Trim() }
    $evidence = [ordered]@{
        schema_version = [int]$latestMigration
        commit_sha = $commit
        status = $status
        image_digests = @($imageDigest) | Where-Object { $_ -match '^sha256:[0-9a-f]{64}$' }
        started_at = $started.ToString("o")
        generated_at = [DateTimeOffset]::UtcNow.ToString("o")
        suites = @($completed)
    }
    $evidence | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $output "commercial-test-evidence.json") -Encoding utf8
}

Write-Host "PASS commercial integration suites=$($completed.Count) commit=$commit migration=$latestMigration"
