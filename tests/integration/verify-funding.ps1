[CmdletBinding()]
param([string]$EnvFile = ".env", [string]$GoExecutable = "go", [switch]$ConfirmIsolatedTestDatabase)
Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"
if (-not $ConfirmIsolatedTestDatabase) { throw "Pass -ConfirmIsolatedTestDatabase for a disposable local Docker database." }
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
if (-not [System.IO.Path]::IsPathRooted($EnvFile)) { $EnvFile = Join-Path $repoRoot $EnvFile }
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) { throw "The environment file does not exist." }
$values = @{}
foreach ($raw in Get-Content -LiteralPath $EnvFile) {
    $line = ([string]$raw).Trim()
    if (-not $line -or $line.StartsWith("#") -or -not $line.Contains("=")) { continue }
    $parts = $line.Split("=", 2); $values[$parts[0].Trim()] = $parts[1].Trim().Trim('"').Trim("'")
}
foreach ($name in @("POSTGRES_USER", "POSTGRES_DB", "DATABASE_URL", "POSTGRES_HOST_PORT")) {
    if (-not $values.ContainsKey($name) -or [string]::IsNullOrWhiteSpace([string]$values[$name])) { throw "The environment file is missing $name." }
}
$postgresHostPort = 0
if (-not [int]::TryParse($values.POSTGRES_HOST_PORT, [ref]$postgresHostPort) -or $postgresHostPort -lt 1 -or $postgresHostPort -gt 65535) {
    throw "POSTGRES_HOST_PORT must be a valid TCP port."
}
$container = "relaydock-postgres-1"
$inspect = docker container inspect $container | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or @($inspect).Count -ne 1 -or -not [bool]$inspect[0].State.Running -or [string]$inspect[0].Config.Labels.'com.docker.compose.service' -ne "postgres") { throw "The expected local RelayDock PostgreSQL service is not running." }
$network = "relaydock_relaydock-internal"
if ($null -eq $inspect[0].NetworkSettings.Networks.PSObject.Properties[$network]) { throw "PostgreSQL is not on the expected internal network." }
$databaseName = "relaydock_funding_test_" + [Guid]::NewGuid().ToString("N").Substring(0, 20)
$created = $false
$oldTestURLExists = Test-Path Env:TEST_DATABASE_URL
$oldTestURL = $env:TEST_DATABASE_URL
try {
    docker container exec $container createdb --no-password --username ([string]$values.POSTGRES_USER) --maintenance-db ([string]$values.POSTGRES_DB) $databaseName | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Creating the disposable funding database failed." }
    $created = $true
    $builder = [System.UriBuilder]([Uri]([string]$values.DATABASE_URL)); $builder.Host = "127.0.0.1"; $builder.Port = $postgresHostPort; $builder.Path = "/$databaseName"
    $env:TEST_DATABASE_URL = $builder.Uri.AbsoluteUri
    & $GoExecutable -C $repoRoot test -count=1 -run TestFundingLedgerIntegration ./internal/store
    if ($LASTEXITCODE -ne 0) { throw "Funding ledger integration test failed." }
    Write-Host "PASS 100 concurrent reservations, idempotent replay, stream cancellation, crash recovery, late usage, immutable balanced replay"
} finally {
    if ($created -and $databaseName -match '^relaydock_funding_test_[0-9a-f]{20}$') {
        docker container exec $container dropdb --no-password --if-exists --force --username ([string]$values.POSTGRES_USER) --maintenance-db ([string]$values.POSTGRES_DB) $databaseName | Out-Null
    }
    if ($oldTestURLExists) { $env:TEST_DATABASE_URL = $oldTestURL } else { Remove-Item Env:TEST_DATABASE_URL -ErrorAction SilentlyContinue }
}
