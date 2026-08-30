[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Version,
    [string]$Commit = "",
    [string]$NotesOutput = "",
    [switch]$RequireApprovedLicense
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$semver = '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$'
if ($Version -notmatch $semver) {
    throw "Release version must conform to Semantic Versioning 2.0."
}
if ($Commit -and $Commit -notmatch '^[0-9a-f]{40}$') {
    throw "Release commit must be a full lowercase 40-character Git SHA."
}

$versionFile = [System.IO.File]::ReadAllText((Join-Path $repoRoot "VERSION")).Trim()
if ($versionFile -cne $Version) {
    throw "VERSION must exactly match release version $Version."
}
$versionSource = [System.IO.File]::ReadAllText((Join-Path $repoRoot "internal\version\version.go"))
$versionMatch = [regex]::Match($versionSource, '(?m)^\s*Current\s*=\s*"([^"]+)"\s*$')
if (-not $versionMatch.Success -or $versionMatch.Groups[1].Value -ne $Version) {
    throw "internal/version.Current must exactly match release version $Version."
}

$changelogPath = Join-Path $repoRoot "CHANGELOG.md"
$changelogLines = [System.IO.File]::ReadAllLines($changelogPath)
$headingPattern = '^## \[' + [regex]::Escape($Version) + '\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$'
$start = -1
for ($index = 0; $index -lt $changelogLines.Length; $index++) {
    if ($changelogLines[$index] -match $headingPattern) {
        $start = $index
        break
    }
}
if ($start -lt 0) {
    throw "CHANGELOG.md must contain a dated entry for $Version."
}
$end = $changelogLines.Length
for ($index = $start + 1; $index -lt $changelogLines.Length; $index++) {
    if ($changelogLines[$index] -match '^## \[') {
        $end = $index
        break
    }
}
$notes = @($changelogLines[$start..($end - 1)])
if (@($notes | Where-Object { $_ -match '^- ' }).Count -eq 0) {
    throw "The changelog entry for $Version has no release items."
}

$licensingPath = Join-Path $repoRoot "docs\licensing-decision.md"
$licensing = [System.IO.File]::ReadAllText($licensingPath)
if ($licensing -notmatch '(?im)^Decision status:\s*\*\*(blocked|approved)\*\*\s*$' -or
    $licensing -notmatch '(?im)^Selected option:\s*\*\*([^*]+)\*\*\s*$') {
    throw "docs/licensing-decision.md does not contain a machine-verifiable decision record."
}
if ($RequireApprovedLicense) {
    $status = [regex]::Match($licensing, '(?im)^Decision status:\s*\*\*([^*]+)\*\*\s*$').Groups[1].Value.Trim().ToLowerInvariant()
    $selection = [regex]::Match($licensing, '(?im)^Selected option:\s*\*\*([^*]+)\*\*\s*$').Groups[1].Value.Trim().ToLowerInvariant()
    $allowedSelections = @("proprietary", "apache-2.0", "agpl-3.0-only", "dual-license")
    if ($status -ne "approved" -or $selection -notin $allowedSelections) {
        throw "Formal release is blocked until the repository owner approves the licensing decision."
    }
    switch ($selection) {
        "proprietary" {
            if (-not (Test-Path -LiteralPath (Join-Path $repoRoot "docs\proprietary-terms.md") -PathType Leaf)) {
                throw "An approved proprietary release requires docs/proprietary-terms.md."
            }
        }
        "apache-2.0" {
            if (-not (Test-Path -LiteralPath (Join-Path $repoRoot "LICENSE") -PathType Leaf)) {
                throw "An approved Apache-2.0 release requires the exact LICENSE text."
            }
        }
        "agpl-3.0-only" {
            if (-not (Test-Path -LiteralPath (Join-Path $repoRoot "LICENSE") -PathType Leaf)) {
                throw "An approved AGPL-3.0-only release requires the exact LICENSE text."
            }
        }
        "dual-license" {
            if (-not (Test-Path -LiteralPath (Join-Path $repoRoot "LICENSE") -PathType Leaf) -or
                -not (Test-Path -LiteralPath (Join-Path $repoRoot "docs\commercial-license.md") -PathType Leaf)) {
                throw "An approved dual-license release requires LICENSE and docs/commercial-license.md."
            }
        }
    }
}

if ($NotesOutput) {
    $outputPath = if ([System.IO.Path]::IsPathRooted($NotesOutput)) {
        [System.IO.Path]::GetFullPath($NotesOutput)
    } else {
        [System.IO.Path]::GetFullPath((Join-Path $repoRoot $NotesOutput))
    }
    if (-not $outputPath.StartsWith($repoRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Release notes output must remain inside the repository workspace."
    }
    [System.IO.Directory]::CreateDirectory([System.IO.Path]::GetDirectoryName($outputPath)) | Out-Null
    [System.IO.File]::WriteAllLines($outputPath, $notes, (New-Object System.Text.UTF8Encoding($false)))
}

Write-Host "PASS release contract for v$Version"
