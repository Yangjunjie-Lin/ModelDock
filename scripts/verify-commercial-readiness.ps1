[CmdletBinding()]
param(
    [ValidateSet("ENGINEERING_PREVIEW", "COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION")]
    [string]$Profile = "ENGINEERING_PREVIEW",
    [string]$Commit = "",
    [string]$ManifestPath = "",
    [string]$TestReportPath = "",
    [string]$ReportOutput = ""
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
if (-not $ManifestPath) { $ManifestPath = Join-Path $repoRoot "release\commercial-gates.yaml" }
if (-not $TestReportPath) { $TestReportPath = Join-Path $repoRoot "release\commercial-test-evidence.json" }
if (-not $ReportOutput) { $ReportOutput = Join-Path $repoRoot "docs\generated\commercial-readiness-report.md" }
if (-not $Commit) { $Commit = (git -C $repoRoot rev-parse HEAD).Trim() }
if ($Commit -notmatch '^[0-9a-f]{40}$') { throw "Commit must be a full lowercase Git SHA." }

$migrationFiles = Get-ChildItem -LiteralPath (Join-Path $repoRoot "migrations") -File -Filter "*.sql"
$migrationVersions = @($migrationFiles | ForEach-Object {
    if ($_.BaseName -match '^(\d{4})_') { [int]$Matches[1] }
})
if ($migrationVersions.Count -eq 0) { throw "No numbered database migration was found." }
$latestMigration = ($migrationVersions | Measure-Object -Maximum).Maximum
$results = [System.Collections.Generic.List[object]]::new()

function Add-Result([string]$Id, [string]$Title, [bool]$Passed, [string]$Detail) {
    $script:results.Add([pscustomobject]@{
        Id = $Id
        Title = $Title
        Result = if ($Passed) { "PASS" } else { "BLOCKED" }
        Detail = $Detail
    })
}

$manifest = $null
try {
    $manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
    $ids = @($manifest.gates | ForEach-Object { [string]$_.id })
    $unique = @($ids | Sort-Object -Unique)
    $manifestValid = $manifest.schema_version -eq 1 -and $ids.Count -eq $unique.Count -and $ids.Count -gt 0
    Add-Result "manifest_schema" "Commercial evidence manifest schema" $manifestValid "schema_version=$($manifest.schema_version); gates=$($ids.Count); unique=$($unique.Count)"
} catch {
    Add-Result "manifest_schema" "Commercial evidence manifest schema" $false $_.Exception.Message
}

$version = ""
$versionPath = Join-Path $repoRoot "VERSION"
if (Test-Path -LiteralPath $versionPath -PathType Leaf) { $version = (Get-Content -LiteralPath $versionPath -Raw).Trim() }
$semver = '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$'
Add-Result "version_source" "Single SemVer version source" ($version -match $semver) "VERSION=$version"

$exactOutput = & (Join-Path $PSHOME "pwsh") -NoProfile -File (Join-Path $repoRoot "scripts\verify-exact-money.ps1") -RepositoryRoot $repoRoot 2>&1 | Out-String
$exactPassed = $LASTEXITCODE -eq 0
Add-Result "exact_money" "Exact-money static gate" $exactPassed $exactOutput.Trim()
Add-Result "latest_migration" "Latest migration is represented" ($latestMigration -ge 24) ("latest={0:D4}" -f [int]$latestMigration)

if ($Profile -ne "ENGINEERING_PREVIEW") {
    $testEvidenceValid = $false
    $testDetail = "Commercial test evidence is missing."
    if (Test-Path -LiteralPath $TestReportPath -PathType Leaf) {
        try {
            $testEvidence = Get-Content -LiteralPath $TestReportPath -Raw | ConvertFrom-Json
            $digests = @($testEvidence.image_digests)
            $digestValid = $digests.Count -gt 0 -and @($digests | Where-Object { $_ -notmatch '^sha256:[0-9a-f]{64}$' }).Count -eq 0
            $testEvidenceValid = [int]$testEvidence.schema_version -eq [int]$latestMigration -and
                [string]$testEvidence.commit_sha -ceq $Commit -and [string]$testEvidence.status -ceq "PASS" -and $digestValid
            $testDetail = "schema_version=$($testEvidence.schema_version); commit=$($testEvidence.commit_sha); status=$($testEvidence.status); digests=$($digests.Count)"
        } catch { $testDetail = $_.Exception.Message }
    }
    Add-Result "commercial_test_evidence" "Exact-commit commercial integration evidence" $testEvidenceValid $testDetail

    $goLive = Get-Content -LiteralPath (Join-Path $repoRoot "docs\go-live-checklist.md") -Raw
    $goLivePassed = $goLive -match '(?im)^\*\*Decision:\s*GO\*\*'
    Add-Result "go_live_decision" "Repository go-live decision" $goLivePassed $(if ($goLivePassed) { "GO" } else { "NO-GO or missing GO decision" })

    if ($null -ne $manifest) {
        $runtime = $manifest.runtime
        $paymentReady = [string]$runtime.payment_adapter -notin @("", "sandbox", "manual_transfer")
        Add-Result "production_payment_adapter" "Production payment adapter" $paymentReady "configured=$($runtime.payment_adapter)"
        $smtpReady = [string]$runtime.smtp_mode -ceq "production"
        Add-Result "production_smtp_mode" "Production SMTP mode" $smtpReady "configured=$($runtime.smtp_mode)"
        $providerReady = [int]$runtime.commercially_approved_provider_count -ge 1
        Add-Result "approved_provider_count" "Commercially approved Provider count" $providerReady "count=$($runtime.commercially_approved_provider_count)"

        if ($Profile -eq "MARKETPLACE_PRODUCTION") {
            $payoutReady = [string]$runtime.payout_adapter -notin @("", "sandbox")
            Add-Result "production_payout_adapter" "Production payout adapter" $payoutReady "configured=$($runtime.payout_adapter)"
            $supplierReady = [int]$runtime.production_ready_supplier_count -ge 1
            Add-Result "production_supplier_count" "Production-ready supplier count" $supplierReady "count=$($runtime.production_ready_supplier_count)"
        }

        foreach ($gate in @($manifest.gates | Where-Object { @($_.profiles) -contains $Profile })) {
            $approvedAt = [DateTimeOffset]::MinValue
            $expiresAt = [DateTimeOffset]::MinValue
            $approvedAtValid = [DateTimeOffset]::TryParse([string]$gate.approved_at, [ref]$approvedAt)
            $expiresAtValid = [DateTimeOffset]::TryParse([string]$gate.expires_at, [ref]$expiresAt)
            $now = [DateTimeOffset]::UtcNow
            $valid = [string]$gate.status -ceq "APPROVED" -and
                -not [string]::IsNullOrWhiteSpace([string]$gate.evidence_reference) -and
                [string]$gate.evidence_sha256 -match '^[0-9a-f]{64}$' -and
                -not [string]::IsNullOrWhiteSpace([string]$gate.approved_by) -and
                $approvedAtValid -and $expiresAtValid -and $approvedAt -le $now -and
                $expiresAt -gt $now -and $expiresAt -gt $approvedAt -and
                [string]$gate.reviewed_commit -ceq $Commit
            $detail = "status=$($gate.status); evidence=$($gate.evidence_reference); expires=$($gate.expires_at); reviewed_commit=$($gate.reviewed_commit)"
            Add-Result ([string]$gate.id) ([string]$gate.title) $valid $detail
        }
    }
}

$blocked = @($results | Where-Object Result -eq "BLOCKED")
$decision = if ($blocked.Count -gt 0) { "NO-GO" } else { "GO — $Profile" }
$generatedAt = [DateTimeOffset]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$lines = [System.Collections.Generic.List[string]]::new()
$lines.Add("# Generated commercial readiness report")
$lines.Add("")
$lines.Add("> Generated by scripts/verify-commercial-readiness.ps1; do not edit manually.")
$lines.Add("")
$lines.Add("- Profile: **$Profile**")
$lines.Add("- Decision: **$decision**")
$lines.Add("- Commit: $Commit")
$lines.Add(("- Latest migration: {0:D4}" -f [int]$latestMigration))
$lines.Add("- Version: $version")
$lines.Add("- Generated at: $generatedAt")
$lines.Add("")
$lines.Add("| Gate | Result | Evidence |")
$lines.Add("| --- | --- | --- |")
foreach ($result in $results) {
    $safe = ([string]$result.Detail).Replace("|", "\|").Replace("`r", " ").Replace("`n", " ")
    $lines.Add("| $($result.Title) [$($result.Id)] | **$($result.Result)** | $safe |")
}
$lines.Add("")
$lines.Add("Any BLOCKED row is fail-closed. Synthetic tests never approve external legal, payment, Provider, supplier, security, tax, or production-operation evidence.")

$outputPath = [System.IO.Path]::GetFullPath($ReportOutput)
if (-not $outputPath.StartsWith($repoRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Generated report output must remain inside the repository."
}
[System.IO.Directory]::CreateDirectory([System.IO.Path]::GetDirectoryName($outputPath)) | Out-Null
[System.IO.File]::WriteAllLines($outputPath, $lines, (New-Object System.Text.UTF8Encoding($false)))

if ($blocked.Count -gt 0) {
    Write-Host "NO-GO $Profile ($($blocked.Count) blocked gate(s)); report: $outputPath"
    exit 1
}
Write-Host "GO $Profile; report: $outputPath"
