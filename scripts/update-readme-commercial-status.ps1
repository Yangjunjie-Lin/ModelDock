[CmdletBinding()]
param([switch]$Check)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$readmePath = Join-Path $repoRoot "README.md"
$cache = Join-Path $repoRoot ".cache\readme-commercial-status"
[System.IO.Directory]::CreateDirectory($cache) | Out-Null
$commit = (git -C $repoRoot rev-parse HEAD).Trim()
$latestMigration = (Get-ChildItem -LiteralPath (Join-Path $repoRoot "migrations") -File -Filter "*.sql" | ForEach-Object {
    if ($_.BaseName -match '^(\d{4})_') { [int]$Matches[1] }
} | Measure-Object -Maximum).Maximum
$pwsh = Join-Path $PSHOME "pwsh"
$verifier = Join-Path $repoRoot "scripts\verify-commercial-readiness.ps1"

function Get-Decision([string]$Profile) {
    $report = Join-Path $cache "$($Profile.ToLowerInvariant()).md"
    & $pwsh -NoProfile -File $verifier -Profile $Profile -Commit $commit -ReportOutput $report *> $null
    $passed = $LASTEXITCODE -eq 0
    if ($passed) { return "GO — $Profile" }
    return "NO-GO"
}

$engineering = Get-Decision "ENGINEERING_PREVIEW"
$commercial = Get-Decision "COMMERCIAL_BETA"
$marketplace = Get-Decision "MARKETPLACE_PRODUCTION"
$date = [DateTimeOffset]::UtcNow.ToString("yyyy-MM-dd")
$block = @"
<!-- commercial-status:start -->
| Release profile | Machine decision |
| --- | --- |
| Engineering implementation | $engineering |
| Commercial operation | $commercial |
| Marketplace production | $marketplace |

Last verified commit: ``$commit`` · Latest migration: ``$('{0:D4}' -f [int]$latestMigration)`` · Evidence date: ``$date``
<!-- commercial-status:end -->
"@.Trim()
$source = [System.IO.File]::ReadAllText($readmePath)
$pattern = '(?s)<!-- commercial-status:start -->.*?<!-- commercial-status:end -->'
if ($source -notmatch $pattern) { throw "README commercial status markers are missing." }
$updated = [regex]::Replace($source, $pattern, [System.Text.RegularExpressions.MatchEvaluator]{ param($match) $block }, 1)
if ($Check) {
    if ($updated -cne $source) { throw "README commercial status is stale; run scripts/update-readme-commercial-status.ps1." }
    Write-Host "PASS README commercial status is machine-current."
    exit 0
}
[System.IO.File]::WriteAllText($readmePath, $updated, (New-Object System.Text.UTF8Encoding($false)))
Write-Host "Updated README commercial status: engineering=$engineering commercial=$commercial marketplace=$marketplace"
exit 0
