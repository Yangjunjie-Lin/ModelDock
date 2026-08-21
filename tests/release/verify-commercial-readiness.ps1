[CmdletBinding()]
param()

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$cache = Join-Path $repoRoot ".cache\release-gate-tests"
[System.IO.Directory]::CreateDirectory($cache) | Out-Null
$commit = (git -C $repoRoot rev-parse HEAD).Trim()
$latestMigration = (Get-ChildItem -LiteralPath (Join-Path $repoRoot "migrations") -File -Filter "*.sql" | ForEach-Object {
    if ($_.BaseName -match '^(\d{4})_') { [int]$Matches[1] }
} | Measure-Object -Maximum).Maximum
$source = Get-Content -LiteralPath (Join-Path $repoRoot "release\commercial-gates.yaml") -Raw | ConvertFrom-Json
$manifestPath = Join-Path $cache "manifest.json"
$evidencePath = Join-Path $cache "evidence.json"
$reportPath = Join-Path $cache "report.md"
$verifier = Join-Path $repoRoot "scripts\verify-commercial-readiness.ps1"
$pwsh = Join-Path $PSHOME "pwsh"
$failures = [System.Collections.Generic.List[string]]::new()

function Copy-Manifest {
    return ($source | ConvertTo-Json -Depth 20 | ConvertFrom-Json)
}

function Approve-Gate($gate, [string]$reviewedCommit = $commit, [DateTimeOffset]$expires = ([DateTimeOffset]::UtcNow.AddDays(30))) {
    $gate.status = "APPROVED"
    $gate.evidence_reference = "TEST-FIXTURE:$($gate.id)"
    $gate.evidence_sha256 = "a" * 64
    $gate.approved_by = "release-gate-test"
    $gate.approved_at = [DateTimeOffset]::UtcNow.AddMinutes(-1).ToString("o")
    $gate.expires_at = $expires.ToString("o")
    $gate.reviewed_commit = $reviewedCommit
}

function Approve-All($manifest, [string]$reviewedCommit = $commit) {
    foreach ($gate in $manifest.gates) { Approve-Gate $gate $reviewedCommit }
    $manifest.runtime.payment_adapter = "contracted-test-fixture"
    $manifest.runtime.payout_adapter = "contracted-payout-test-fixture"
    $manifest.runtime.smtp_mode = "production"
    $manifest.runtime.commercially_approved_provider_count = 1
    $manifest.runtime.production_ready_supplier_count = 1
}

function Write-Evidence([int]$schema = $latestMigration, [string]$evidenceCommit = $commit) {
    [ordered]@{
        schema_version = $schema
        commit_sha = $evidenceCommit
        status = "PASS"
        image_digests = @("sha256:" + ("b" * 64))
        generated_at = [DateTimeOffset]::UtcNow.ToString("o")
    } | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $evidencePath -Encoding utf8
}

function Invoke-Scenario([string]$name, $manifest, [string]$profile, [string]$expectedReportText) {
    $manifest | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $manifestPath -Encoding utf8
    & $pwsh -NoProfile -File $verifier -Profile $profile -Commit $commit -ManifestPath $manifestPath -TestReportPath $evidencePath -ReportOutput $reportPath *> $null
    if ($LASTEXITCODE -eq 0) {
        $failures.Add("$name unexpectedly passed")
        return
    }
    $report = Get-Content -LiteralPath $reportPath -Raw
    if ($report -notmatch [regex]::Escape($expectedReportText)) {
        $failures.Add("$name did not report expected blocker: $expectedReportText")
    }
}

Write-Evidence

$licenseOnly = Copy-Manifest
Approve-Gate ($licenseOnly.gates | Where-Object id -eq "software_license")
Invoke-Scenario "license-only approval" $licenseOnly "COMMERCIAL_BETA" "Production payment-provider agreement"

$migration19 = Copy-Manifest
Approve-All $migration19
Write-Evidence -schema 19
Invoke-Scenario "migration-19 stale report" $migration19 "COMMERCIAL_BETA" "schema_version=19"

$differentTestCommit = Copy-Manifest
Approve-All $differentTestCommit
Write-Evidence -evidenceCommit ("c" * 40)
Invoke-Scenario "different test commit" $differentTestCommit "COMMERCIAL_BETA" ("commit=" + ("c" * 40))

Write-Evidence
$sandboxPayment = Copy-Manifest
Approve-All $sandboxPayment
$sandboxPayment.runtime.payment_adapter = "sandbox"
Invoke-Scenario "sandbox payment" $sandboxPayment "COMMERCIAL_BETA" "configured=sandbox"

$missingProvider = Copy-Manifest
Approve-All $missingProvider
$providerGate = $missingProvider.gates | Where-Object id -eq "provider_commercial_rights"
$providerGate.status = "BLOCKED"
Invoke-Scenario "missing Provider contract" $missingProvider "COMMERCIAL_BETA" "At least one Provider commercial distribution approval"

$expired = Copy-Manifest
Approve-All $expired
Approve-Gate ($expired.gates | Where-Object id -eq "finance_signoff") $commit ([DateTimeOffset]::UtcNow.AddMinutes(-1))
Invoke-Scenario "expired approval" $expired "COMMERCIAL_BETA" "Finance owner sign-off"

$wrongCommit = Copy-Manifest
Approve-All $wrongCommit ("d" * 40)
Invoke-Scenario "approval for another commit" $wrongCommit "COMMERCIAL_BETA" ("reviewed_commit=" + ("d" * 40))

$futureApproval = Copy-Manifest
Approve-All $futureApproval
$futureGate = $futureApproval.gates | Where-Object id -eq "security_signoff"
$futureGate.approved_at = [DateTimeOffset]::UtcNow.AddDays(1).ToString("o")
Invoke-Scenario "future-dated approval" $futureApproval "COMMERCIAL_BETA" "Security owner sign-off"

$sandboxPayout = Copy-Manifest
Approve-All $sandboxPayout
$sandboxPayout.runtime.payout_adapter = "sandbox"
Invoke-Scenario "sandbox payout" $sandboxPayout "MARKETPLACE_PRODUCTION" "configured=sandbox"

if ($failures.Count -gt 0) {
    $failures | ForEach-Object { Write-Error $_ }
    throw "$($failures.Count) commercial release-gate test(s) failed."
}
Write-Host "PASS commercial release-gate negative tests (9 scenarios)."
$global:LASTEXITCODE = 0
