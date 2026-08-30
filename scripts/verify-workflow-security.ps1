[CmdletBinding()]
param()

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$workflowRoot = Join-Path $repoRoot ".github/workflows"
$failures = [System.Collections.Generic.List[string]]::new()
foreach ($file in Get-ChildItem -LiteralPath $workflowRoot -File -Filter "*.yml") {
    $text = Get-Content -LiteralPath $file.FullName -Raw
    if ($text -notmatch '(?m)^permissions:\s*\r?\n\s{2}contents:\s*read\s*$') {
        $failures.Add("$($file.Name): top-level permissions must be contents: read")
    }
    if ($text -match '(?m)^\s{2}(packages|id-token|attestations|pull-requests|actions|checks):\s*write\s*$') {
        $failures.Add("$($file.Name): write permission is granted at workflow scope")
    }
    foreach ($match in [regex]::Matches($text, '(?m)^\s*-?\s*uses:\s*([^\s#]+)')) {
        $uses = $match.Groups[1].Value
        if ($uses.StartsWith('./')) { continue }
        if ($uses -notmatch '@[0-9a-f]{40}$') { $failures.Add("$($file.Name): external Action is not pinned to a full commit: $uses") }
    }
}
$releaseWorkflow = Get-Content -LiteralPath (Join-Path $workflowRoot "release.yml") -Raw
$safePushProfile = 'RELEASE_PROFILE: ${{ github.event_name == ''workflow_dispatch'' && inputs.release_profile || ''ENGINEERING_PREVIEW'' }}'
if (-not $releaseWorkflow.Contains($safePushProfile, [System.StringComparison]::Ordinal)) {
    $failures.Add("release.yml: Tag pushes must default to ENGINEERING_PREVIEW and commercial profiles must require workflow_dispatch")
}
if ($releaseWorkflow.Contains("MODELDOCK_RELEASE_PROFILE", [System.StringComparison]::Ordinal)) {
    $failures.Add("release.yml: mutable repository variables must not select a Tag-push release profile")
}
if (-not $releaseWorkflow.Contains("./scripts/verify-release-tag.ps1 -Ref `$env:GITHUB_REF -RefType `$env:GITHUB_REF_TYPE -Commit `$env:GITHUB_SHA", [System.StringComparison]::Ordinal)) {
    $failures.Add("release.yml: exact remote annotated Tag verification is missing")
}
if ($failures.Count -gt 0) {
    $failures | ForEach-Object { Write-Error $_ }
    throw "$($failures.Count) workflow permission/pinning issue(s)."
}
Write-Host "PASS workflow Actions are commit-pinned and write permissions are job-scoped."
