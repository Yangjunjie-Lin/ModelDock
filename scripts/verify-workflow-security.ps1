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
if ($failures.Count -gt 0) {
    $failures | ForEach-Object { Write-Error $_ }
    throw "$($failures.Count) workflow permission/pinning issue(s)."
}
Write-Host "PASS workflow Actions are commit-pinned and write permissions are job-scoped."
