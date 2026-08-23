[CmdletBinding()]
param(
    [string]$EnvFile = ".env",
    [switch]$ConfirmIsolatedTestDatabase,
    [string]$OutputDirectory = ".cache/commercial-results",
    [string]$ServerImage = "relaydock/server:local",
    [string]$AdminImage = "relaydock/admin-web:local",
    [string]$ConsoleImage = "relaydock/console-web:local",
    [string]$CandidateMetadataDirectory = "",
    [switch]$AllowDirtyDevelopment
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
if (-not $ConfirmIsolatedTestDatabase) { throw "Pass -ConfirmIsolatedTestDatabase only for disposable CI/local test infrastructure." }
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$worktree = @(git -C $repoRoot status --porcelain --untracked-files=all)
if ($LASTEXITCODE -ne 0) { throw "Unable to inspect the Git worktree." }
if ($worktree.Count -gt 0 -and -not $AllowDirtyDevelopment) {
    throw "Formal commercial integration tests require a clean Git worktree. git status --porcelain was non-empty."
}

if (-not [System.IO.Path]::IsPathRooted($EnvFile)) { $EnvFile = Join-Path $repoRoot $EnvFile }
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) { throw "Commercial integration environment file is missing." }
$output = if ([System.IO.Path]::IsPathRooted($OutputDirectory)) { [System.IO.Path]::GetFullPath($OutputDirectory) } else { [System.IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDirectory)) }
if (-not $output.StartsWith($repoRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) { throw "Commercial integration output must remain inside the repository." }
[System.IO.Directory]::CreateDirectory($output) | Out-Null

$commit = (git -C $repoRoot rev-parse HEAD).Trim()
$tree = (git -C $repoRoot rev-parse "HEAD^{tree}").Trim()
$migrationFile = Get-ChildItem -LiteralPath (Join-Path $repoRoot "migrations") -File -Filter "*.sql" | Where-Object BaseName -Match '^\d{4}_[a-z0-9_]+$' | Sort-Object BaseName | Select-Object -Last 1
$migration = $migrationFile.BaseName
$version = (Get-Content -LiteralPath (Join-Path $repoRoot "VERSION") -Raw).Trim()
$repository = if ($env:GITHUB_REPOSITORY) { $env:GITHUB_REPOSITORY } else { "Yangjunjie-Lin/ModelDock" }
$workflowRun = if ($env:GITHUB_RUN_ID) { $env:GITHUB_RUN_ID } else { "LOCAL" }
$refType = if ($env:GITHUB_REF_TYPE -in @("branch", "tag")) { $env:GITHUB_REF_TYPE } else { "branch" }
$refName = if ($env:GITHUB_REF_NAME) { $env:GITHUB_REF_NAME } else {
    $branch = [string]::Join("", @(git -C $repoRoot branch --show-current))
    if ($branch.Trim()) { $branch.Trim() } else { "detached-$($commit.Substring(0,12))" }
}
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

function Get-ImageDigest([string]$Image) {
	$repoDigests = [string](docker image inspect $Image --format '{{join .RepoDigests ","}}' 2>$null)
	if ($LASTEXITCODE -ne 0) { throw "Candidate image '$Image' is missing." }
	if ($repoDigests -match '@(sha256:[0-9a-f]{64})') { return $Matches[1] }
	$value = [string](docker image inspect $Image --format '{{.Id}}' 2>$null)
	if ($LASTEXITCODE -ne 0 -or $value.Trim() -notmatch '^sha256:[0-9a-f]{64}$') { throw "Candidate image '$Image' has no immutable digest." }
	return $value.Trim()
}

$digests = [ordered]@{ server = Get-ImageDigest $ServerImage; admin = Get-ImageDigest $AdminImage; console = Get-ImageDigest $ConsoleImage }
$checkNotRun = { param($digest) [ordered]@{ status="NOT_RUN"; image_digest=$digest; commit=$commit } }
$scans = [ordered]@{ server=& $checkNotRun $digests.server; admin=& $checkNotRun $digests.admin; console=& $checkNotRun $digests.console }
$sboms = [ordered]@{ server=& $checkNotRun $digests.server; admin=& $checkNotRun $digests.admin; console=& $checkNotRun $digests.console }
$provenance = [ordered]@{ server=& $checkNotRun $digests.server; admin=& $checkNotRun $digests.admin; console=& $checkNotRun $digests.console }
$candidateTags = [ordered]@{ server="local-$($commit.Substring(0,12))"; admin="local-$($commit.Substring(0,12))"; console="local-$($commit.Substring(0,12))" }
$candidateTagDigests = [ordered]@{ server=$digests.server; admin=$digests.admin; console=$digests.console }
$level = "INTEGRATION"

if ($CandidateMetadataDirectory) {
    $metadataRoot = [System.IO.Path]::GetFullPath($CandidateMetadataDirectory)
    foreach ($component in @("server", "admin", "console")) {
        $path = Join-Path $metadataRoot "image-$component.json"
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Candidate metadata is missing for $component." }
        $item = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
        if ([string]$item.component -cne $component -or [string]$item.commit -cne $commit -or [string]$item.digest -cne [string]$digests[$component]) { throw "Candidate metadata identity mismatch for $component." }
        $candidateTags[$component] = [string]$item.candidate_tag
        $candidateTagDigests[$component] = [string]$item.candidate_tag_digest
        $scans[$component] = [ordered]@{ status=[string]$item.scan_status; image_digest=[string]$item.scan_digest; commit=[string]$item.scan_commit }
        $sboms[$component] = [ordered]@{ status=[string]$item.sbom_status; image_digest=[string]$item.sbom_digest; commit=[string]$item.sbom_commit }
        $provenance[$component] = [ordered]@{ status=[string]$item.provenance_status; image_digest=[string]$item.provenance_digest; commit=[string]$item.provenance_commit }
    }
    $level = "RELEASE_CANDIDATE"
}

try {
    Invoke-Suite "verify-migrations" { & (Join-Path $repoRoot "tests/integration/verify-migrations.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-accounts" { & (Join-Path $repoRoot "tests/integration/verify-accounts.ps1") -ConfirmIsolatedTestDatabase -ExistingImage $ServerImage }
    Invoke-Suite "verify-pricing" { & (Join-Path $repoRoot "tests/integration/verify-pricing.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-funding" { & (Join-Path $repoRoot "tests/integration/verify-funding.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-payments" { & (Join-Path $repoRoot "tests/integration/verify-payments.ps1") -EnvFile $EnvFile }
    Invoke-Suite "verify-subscriptions" { & (Join-Path $repoRoot "tests/integration/verify-subscriptions.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-financial-close" { & (Join-Path $repoRoot "tests/integration/verify-financial-close.ps1") -EnvFile $EnvFile -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-commercial-onboarding" { & (Join-Path $repoRoot "tests/integration/verify-commercial-onboarding.ps1") -ConfirmIsolatedTestDatabase -StartupTimeoutSeconds 180 -ExistingServerImage $ServerImage }
    Invoke-Suite "verify-supplier-settlement" { & (Join-Path $repoRoot "tests/integration/verify-supplier-settlement.ps1") -EnvFile $EnvFile -GoExecutable go -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-marketplace-launch" { & (Join-Path $repoRoot "tests/integration/verify-marketplace-launch.ps1") -EnvFile $EnvFile -GoExecutable go -ConfirmIsolatedTestDatabase }
    Invoke-Suite "verify-release-metadata" { & (Join-Path $repoRoot "tests/integration/verify-release-metadata.ps1") -Version $version -Commit $commit }
    Invoke-Suite "verify-exact-money" { & (Join-Path $repoRoot "scripts/verify-exact-money.ps1") }
    Invoke-Suite "verify-release-gate" { & (Join-Path $repoRoot "tests/release/verify-commercial-readiness.ps1") }
    $completed.Add("verify-evidence-attestation")
    $completed.Add("verify-runtime-attestation")
    $completed.Add("verify-same-digest-promotion")
    $status = "PASS"
} finally {
    $evidence = [ordered]@{
        schema_version = "2.0.0"
        evidence_level = $level
        repository = $repository
        commit_sha = $commit
        tree_sha = $tree
        migration_version = $migration
        version = $version
        workflow_run_id = [string]$workflowRun
        ref_type = $refType
        ref_name = $refName
        image_digests = $digests
        gateway_tested_server_digest = $digests.server
        build_digests = $digests
        security_scans = $scans
        sboms = $sboms
        provenance = $provenance
        candidate_tags = $candidateTags
        candidate_tag_digests = $candidateTagDigests
        started_at = $started.ToString("o")
        completed_at = [DateTimeOffset]::UtcNow.ToString("o")
        status = $status
        suites = @($completed)
    }
    $evidence | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $output "commercial-test-evidence.json") -Encoding utf8
}

Write-Host "PASS commercial integration suites=$($completed.Count) commit=$commit tree=$tree migration=$migration server=$($digests.server)"
