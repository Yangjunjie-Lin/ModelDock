[CmdletBinding()]
param(
    [ValidateSet("ENGINEERING_PREVIEW", "COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION")]
    [string]$Profile = "ENGINEERING_PREVIEW",
    [string]$Commit = "",
    [string]$ManifestPath = "",
    [string]$TestReportPath = "",
    [string]$EvidenceRoot = "",
    [string]$TrustPolicyPath = "",
    [string]$ReportOutput = "",
    [string]$GoLiveOutput = "",
    [string]$Repository = "",
    [switch]$AllowDirtyDevelopment
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
if (-not $ManifestPath) { $ManifestPath = Join-Path $repoRoot "release/commercial-gates.yaml" }
if (-not $TrustPolicyPath) { $TrustPolicyPath = Join-Path $repoRoot "release/trusted-attestation-issuers.json" }
if (-not $EvidenceRoot) { $EvidenceRoot = Join-Path $repoRoot ".cache/release-evidence" }
if (-not $ReportOutput) { $ReportOutput = Join-Path $repoRoot ".cache/commercial-readiness-report.md" }
if (-not $GoLiveOutput) { $GoLiveOutput = Join-Path $repoRoot ".cache/go-live-checklist.md" }

$status = @(git -C $repoRoot status --porcelain --untracked-files=all)
if ($LASTEXITCODE -ne 0) { throw "Unable to inspect Git worktree status." }
if ($status.Count -gt 0 -and -not $AllowDirtyDevelopment) {
    throw "Formal commercial verification requires a clean Git worktree. git status --porcelain was non-empty."
}

$head = (git -C $repoRoot rev-parse HEAD).Trim()
$tree = (git -C $repoRoot rev-parse "HEAD^{tree}").Trim()
if (-not $Commit) { $Commit = $head }
if ($Commit -notmatch '^[0-9a-f]{40}$' -or $Commit -cne $head) { throw "Commit must equal the full lowercase checkout HEAD." }
if ($tree -notmatch '^[0-9a-f]{40}$') { throw "Unable to resolve the Git Tree SHA." }

$migrationFile = Get-ChildItem -LiteralPath (Join-Path $repoRoot "migrations") -File -Filter "*.sql" |
    Where-Object BaseName -Match '^\d{4}_[a-z0-9_]+$' | Sort-Object BaseName | Select-Object -Last 1
if ($null -eq $migrationFile) { throw "No numbered database migration was found." }
$latestMigration = $migrationFile.BaseName
$version = (Get-Content -LiteralPath (Join-Path $repoRoot "VERSION") -Raw).Trim()
$semver = '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$'
if ($version -notmatch $semver) { throw "VERSION is not Semantic Version 2.0." }

$manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
if (-not $Repository) { $Repository = if ($env:GITHUB_REPOSITORY) { $env:GITHUB_REPOSITORY } else { [string]$manifest.repository } }
if ($Repository -cne [string]$manifest.repository) { throw "Repository identity does not match the commercial manifest." }

$cache = Join-Path $repoRoot ".cache/commercial-readiness"
[System.IO.Directory]::CreateDirectory($cache) | Out-Null
[System.IO.Directory]::CreateDirectory($EvidenceRoot) | Out-Null
$validatorOutput = Join-Path $cache "validator-$($Profile.ToLowerInvariant()).json"
$nodeArgs = @(
    (Join-Path $repoRoot "scripts/verify-commercial-evidence.mjs"),
    "--manifest", $ManifestPath,
    "--schema", (Join-Path $repoRoot "release/commercial-gates.schema.json"),
    "--test-schema", (Join-Path $repoRoot "release/commercial-test-evidence.schema.json"),
    "--trust-schema", (Join-Path $repoRoot "release/trusted-attestation-issuers.schema.json"),
    "--trust-policy", $TrustPolicyPath,
    "--profile", $Profile,
    "--repository", $Repository,
    "--commit", $Commit,
    "--tree", $tree,
    "--version", $version,
    "--migration", $latestMigration,
    "--evidence-root", $EvidenceRoot,
    "--output", $validatorOutput
)
if ($TestReportPath) { $nodeArgs += @("--test-evidence", $TestReportPath) }
if ($Profile -ne "ENGINEERING_PREVIEW") {
    $nodeArgs += @("--require-trust-anchor", "true")
    if ($env:MODELDOCK_TRUST_POLICY_SHA256) { $nodeArgs += @("--trust-policy-sha256", $env:MODELDOCK_TRUST_POLICY_SHA256) }
}
& node @nodeArgs
$validatorExit = $LASTEXITCODE
if (-not (Test-Path -LiteralPath $validatorOutput -PathType Leaf)) { throw "Evidence validator did not produce a result." }
$validated = Get-Content -LiteralPath $validatorOutput -Raw | ConvertFrom-Json

$results = [System.Collections.Generic.List[object]]::new()
foreach ($result in @($validated.results)) { $results.Add($result) }
function Add-Result([string]$Id, [string]$Title, [bool]$Passed, [string]$Detail) {
    $script:results.Add([pscustomobject]@{ id=$Id; title=$Title; result=$(if ($Passed) { "PASS" } else { "BLOCKED" }); detail=$Detail })
}

$exactOutput = & (Join-Path $PSHOME "pwsh") -NoProfile -File (Join-Path $repoRoot "scripts/verify-exact-money.ps1") -RepositoryRoot $repoRoot 2>&1 | Out-String
Add-Result "exact_money" "Exact-money static gate" ($LASTEXITCODE -eq 0) $exactOutput.Trim()
Add-Result "git_identity" "Clean exact Git commit and tree" ($status.Count -eq 0) "commit=$Commit; tree=$tree; clean=$($status.Count -eq 0)"
Add-Result "latest_migration" "Latest migration identity" ($latestMigration -match '^0024_') "migration=$latestMigration"
Add-Result "version_source" "Single SemVer version source" ($version -match $semver) "VERSION=$version"

$blocked = @($results | Where-Object result -eq "BLOCKED")
$decision = if ($blocked.Count -gt 0) { "NO-GO" } else { "GO — $Profile" }
$generatedAt = [DateTimeOffset]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$workflowRun = if ($env:GITHUB_RUN_ID) { $env:GITHUB_RUN_ID } else { "LOCAL" }
$refType = if ($env:GITHUB_REF_TYPE) { $env:GITHUB_REF_TYPE } else { "branch" }
$refName = if ($env:GITHUB_REF_NAME) { $env:GITHUB_REF_NAME } else { (git -C $repoRoot branch --show-current).Trim() }

function Write-Report([string]$Path, [bool]$GoLive) {
    $lines = [System.Collections.Generic.List[string]]::new()
    $lines.Add($(if ($GoLive) { "# ModelDock generated go-live decision" } else { "# Generated commercial readiness report" }))
    $lines.Add("")
    $lines.Add("> Generated only from machine validation; do not edit manually or use the tracked documentation snapshot as release evidence.")
    $lines.Add("")
    $lines.Add("- Profile: **$Profile**")
    $lines.Add("- Decision: **$decision**")
    $lines.Add("- Repository: $Repository")
    $lines.Add("- Commit: $Commit")
    $lines.Add("- Git tree: $tree")
    $lines.Add("- Latest migration: $latestMigration")
    $lines.Add("- Version: $version")
    $lines.Add("- Workflow run ID: $workflowRun")
    $lines.Add("- Ref: $refType/$refName")
    $lines.Add("- Generated at: $generatedAt")
    $lines.Add("")
    $lines.Add("| Gate | Result | Source / signature / exact identity |")
    $lines.Add("| --- | --- | --- |")
    foreach ($result in $results) {
        $detail = ([string]$result.detail).Replace("|", "\|").Replace("`r", " ").Replace("`n", " ")
        $source = if ($result.PSObject.Properties.Name -contains "source") { [string]$result.source } else { "machine" }
        $signature = if ($result.PSObject.Properties.Name -contains "signature_status") { [string]$result.signature_status } else { "N/A" }
        $expires = if ($result.PSObject.Properties.Name -contains "expires_at") { [string]$result.expires_at } else { "" }
        $lines.Add("| $($result.title) [$($result.id)] | **$($result.result)** | source=$source; signature=$signature; commit=$Commit; expires=$expires; $detail |")
    }
    $lines.Add("")
    $lines.Add("The decision is a pure output. Any BLOCKED or NOT RUN prerequisite produces NO-GO. No document Decision field is read as an input.")
    $full = [System.IO.Path]::GetFullPath($Path)
    if (-not $full.StartsWith($repoRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) { throw "Report output must remain inside the repository." }
    [System.IO.Directory]::CreateDirectory([System.IO.Path]::GetDirectoryName($full)) | Out-Null
    [System.IO.File]::WriteAllLines($full, $lines, (New-Object System.Text.UTF8Encoding($false)))
}

Write-Report $ReportOutput $false
Write-Report $GoLiveOutput $true
if ($blocked.Count -gt 0 -or $validatorExit -ne 0) {
    Write-Host "NO-GO $Profile ($($blocked.Count) blocked gate(s)); report=$ReportOutput"
    exit 1
}
Write-Host "GO $Profile commit=$Commit tree=$tree; report=$ReportOutput"
