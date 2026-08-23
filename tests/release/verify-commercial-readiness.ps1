[CmdletBinding()]
param()

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "../.."))
$test = Join-Path $repoRoot "tests/release/commercial-evidence.test.mjs"
& node $test
if ($LASTEXITCODE -ne 0) { throw "Commercial evidence V2 negative tests failed." }
$global:LASTEXITCODE = 0
