[CmdletBinding()]
param(
    [string]$EnvFile = ".env",
    [string]$GoExecutable = "go"
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$envPath = if ([System.IO.Path]::IsPathRooted($EnvFile)) {
    [System.IO.Path]::GetFullPath($EnvFile)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $repoRoot $EnvFile))
}
if (-not (Test-Path -LiteralPath $envPath -PathType Leaf)) { throw "The payment integration environment file does not exist." }
$values = @{}
Get-Content -LiteralPath $envPath | ForEach-Object {
    if ($_ -match '^\s*([^#=]+)=(.*)$') { $values[$matches[1].Trim()] = $matches[2].Trim() }
}
foreach ($name in @("POSTGRES_USER", "POSTGRES_DB", "DATABASE_URL", "POSTGRES_HOST_PORT")) {
    if (-not $values.ContainsKey($name) -or [string]::IsNullOrWhiteSpace($values[$name])) { throw "$name is required in the local untracked .env." }
}
$postgresHostPort = 0
if (-not [int]::TryParse($values.POSTGRES_HOST_PORT, [ref]$postgresHostPort) -or $postgresHostPort -lt 1 -or $postgresHostPort -gt 65535) {
    throw "POSTGRES_HOST_PORT must be a valid TCP port."
}
$run = [Guid]::NewGuid().ToString("N").Substring(0, 20)
$databaseName = "relaydock_payment_test_$run"
$container = (docker compose --env-file $envPath ps -q postgres).Trim()
if (-not $container) { throw "The local Compose PostgreSQL service must be running." }
$created = $false
$oldExists = Test-Path Env:TEST_DATABASE_URL
$oldValue = $env:TEST_DATABASE_URL
try {
    docker exec $container createdb -U $values.POSTGRES_USER $databaseName
    if ($LASTEXITCODE -ne 0) { throw "Could not create the disposable payment database." }
    $created = $true
    $builder = [System.UriBuilder]([Uri]([string]$values.DATABASE_URL)); $builder.Host = "127.0.0.1"; $builder.Port = $postgresHostPort; $builder.Path = "/$databaseName"
    $env:TEST_DATABASE_URL = $builder.Uri.AbsoluteUri
    & $GoExecutable -C $repoRoot test -count=1 -run TestPaymentOrdersIntegration ./internal/store
    if ($LASTEXITCODE -ne 0) { throw "Payment integration test failed." }
    Write-Host "PASS payment webhook replay, failure isolation, crash recovery, and bidirectional traceability"
} finally {
    if ($created -and $databaseName -match '^relaydock_payment_test_[0-9a-f]{20}$') {
        docker exec $container dropdb -U $values.POSTGRES_USER --if-exists $databaseName | Out-Null
    }
    if ($oldExists) { $env:TEST_DATABASE_URL = $oldValue } else { Remove-Item Env:TEST_DATABASE_URL -ErrorAction SilentlyContinue }
}
