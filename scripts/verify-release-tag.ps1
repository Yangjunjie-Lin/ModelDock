[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Ref,
    [Parameter(Mandatory = $true)]
    [string]$RefType,
    [Parameter(Mandatory = $true)]
    [string]$Commit,
    [string]$RepositoryRoot = ""
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
if (Test-Path variable:PSNativeCommandUseErrorActionPreference) {
    $PSNativeCommandUseErrorActionPreference = $false
}

$repoRoot = if ($RepositoryRoot) {
    [System.IO.Path]::GetFullPath($RepositoryRoot)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}
if (-not (Test-Path -LiteralPath (Join-Path $repoRoot ".git"))) {
    throw "Release Tag verification requires a Git worktree."
}
if ($Commit -notmatch '^[0-9a-f]{40}$') {
    throw "Release commit must be a full lowercase 40-character Git SHA."
}
if ($RefType -cne "tag" -or -not $Ref.StartsWith("refs/tags/", [System.StringComparison]::Ordinal)) {
    throw "Release workflows require a pre-existing annotated Git Tag ref."
}

& git -C $repoRoot check-ref-format $Ref *> $null
if ($LASTEXITCODE -ne 0) {
    throw "Release Tag ref is not a valid Git ref."
}

# actions/checkout can leave refs/tags/<name> pointing directly at the peeled
# commit. Restore the exact remote Tag object before deciding whether the Tag is
# annotated. Fetch only this Tag so unrelated remote refs cannot affect the
# release identity check.
$refSpec = "+${Ref}:${Ref}"
& git -C $repoRoot fetch --no-tags --force --quiet origin $refSpec
if ($LASTEXITCODE -ne 0) {
    throw "Could not restore the exact release Tag object from origin."
}

$objectType = [string]::Join("", @(& git -C $repoRoot cat-file -t $Ref 2>$null)).Trim()
if ($LASTEXITCODE -ne 0 -or $objectType -cne "tag") {
    throw "Release workflows require a pre-existing annotated Git Tag."
}

$tagCommit = [string]::Join("", @(& git -C $repoRoot rev-parse "${Ref}^{commit}" 2>$null)).Trim()
if ($LASTEXITCODE -ne 0 -or $tagCommit -notmatch '^[0-9a-f]{40}$') {
    throw "Annotated release Tag does not resolve to a commit."
}
if ($tagCommit -cne $Commit) {
    throw "Annotated release Tag does not resolve to the workflow commit."
}

Write-Host "PASS annotated release Tag resolves to exact commit $Commit"
