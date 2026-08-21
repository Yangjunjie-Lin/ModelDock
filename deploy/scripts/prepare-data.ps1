[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$paths = @(
    (Join-Path $projectRoot "data\postgres"),
    (Join-Path $projectRoot "data\redis"),
    (Join-Path $projectRoot "data\prometheus"),
    (Join-Path $projectRoot "data\alertmanager"),
    (Join-Path $projectRoot "logs")
)

foreach ($path in $paths) {
    New-Item -ItemType Directory -Force -Path $path | Out-Null
    Write-Host "Ready: $path"
}

Write-Host "Runtime data will remain under $projectRoot"
