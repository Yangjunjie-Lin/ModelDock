[CmdletBinding()]
param(
    [string]$EnvFile = ".env",
    [string]$GoExecutable = "go",
    [switch]$ConfirmIsolatedTestDatabase
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"
if (-not $ConfirmIsolatedTestDatabase) { throw "Pass -ConfirmIsolatedTestDatabase for this disposable local Docker test." }
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
if (-not [IO.Path]::IsPathRooted($EnvFile)) { $EnvFile = Join-Path $repo $EnvFile }
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) { throw "Environment file not found." }

$settings = @{}
foreach ($raw in Get-Content -LiteralPath $EnvFile) {
    $line = $raw.Trim()
    if (-not $line -or $line.StartsWith("#") -or -not $line.Contains("=")) { continue }
    $name, $value = $line.Split("=", 2)
    $settings[$name.Trim()] = $value.Trim().Trim('"').Trim("'")
}
foreach ($required in @("POSTGRES_USER", "POSTGRES_DB", "DATABASE_URL")) {
    if (-not $settings.ContainsKey($required) -or [string]::IsNullOrWhiteSpace($settings[$required])) { throw "Missing $required." }
}
$postgres = @(docker ps --filter "ancestor=postgres:17-alpine" --format "{{.ID}}") | Select-Object -First 1
if ([string]::IsNullOrWhiteSpace($postgres)) { throw "The local RelayDock PostgreSQL container is not running." }
$database = "relayedock_settlement_test_" + [Guid]::NewGuid().ToString("N").Substring(0, 16)
if ($database -notmatch '^relayedock_settlement_test_[0-9a-f]{16}$') { throw "Unsafe generated database name." }
$created = $false
$oldTestURL = $env:TEST_DATABASE_URL
$oldDatabaseURL = $env:DATABASE_URL

function Invoke-Psql([string]$Sql) {
    $result = docker exec $postgres psql --no-psqlrc --no-align --tuples-only --quiet --no-password --set=ON_ERROR_STOP=1 -U $settings.POSTGRES_USER -d $database -c $Sql 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Disposable database verification failed; PostgreSQL diagnostics were suppressed." }
    return [string]::Join("`n", @($result))
}

try {
    docker exec $postgres createdb --no-password -U $settings.POSTGRES_USER --maintenance-db $settings.POSTGRES_DB $database | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not create the disposable database." }
    $created = $true

    Invoke-Psql "CREATE TABLE schema_migrations(version bigint PRIMARY KEY,name text NOT NULL,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now());" | Out-Null
    foreach ($version in 1..21) {
        $file = Get-ChildItem (Join-Path $repo "migrations") -Filter ("{0:D4}_*.sql" -f $version) -File
        if (@($file).Count -ne 1) { throw "Migration $version was not found exactly once." }
        Get-Content -Raw -LiteralPath $file.FullName | docker exec -i $postgres psql --no-psqlrc --quiet --no-password --set=ON_ERROR_STOP=1 -U $settings.POSTGRES_USER -d $database 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "Applying the version-21 upgrade fixture failed at migration $version; diagnostics were suppressed." }
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash.ToLowerInvariant()
        $name = $file.BaseName.Substring(5)
        Invoke-Psql "INSERT INTO schema_migrations(version,name,checksum) VALUES($version,'$name','$hash');" | Out-Null
    }
    Invoke-Psql "INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at) VALUES('22000000-0000-4000-8000-000000000001','settlement-upgrade@example.invalid','synthetic-hash','Synthetic Upgrade Supplier','ADMIN','ACTIVE',now()); INSERT INTO organizations(id,name,slug,status,billing_region,metadata) VALUES('22000000-0000-4000-8000-000000000002','Synthetic Upgrade Supplier','synthetic-upgrade-supplier','ACTIVE','US','{}'); BEGIN; SELECT set_config('relaydock.supplier_admin_action','true',true); INSERT INTO supplier_organizations(id,organization_id,owner_user_id,legal_name,display_name,registration_number,incorporation_country,kyb_status,contract_status,status,payout_currency) VALUES('22000000-0000-4000-8000-000000000003','22000000-0000-4000-8000-000000000002','22000000-0000-4000-8000-000000000001','Synthetic Upgrade Supplier','Synthetic Upgrade Supplier','SYNTHETIC-UPGRADE','US','VERIFIED','ACTIVE','APPROVED','USD'); COMMIT;" | Out-Null

    $uri = [Uri]$settings.DATABASE_URL
    $builder = [UriBuilder]$uri
    $builder.Host = "127.0.0.1"
    $builder.Port = 5433
    $builder.Path = "/$database"
    $env:DATABASE_URL = $builder.Uri.AbsoluteUri
    $env:TEST_DATABASE_URL = $builder.Uri.AbsoluteUri
    & $GoExecutable run ./cmd/relayedock migrate
    if ($LASTEXITCODE -ne 0) { throw "The V21 to V22 application migration failed." }

    $upgrade = (Invoke-Psql "SELECT (SELECT count(*) FROM schema_migrations WHERE version=22 AND name='supplier_settlement')||'|'||(SELECT count(*) FROM supplier_organizations WHERE id='22000000-0000-4000-8000-000000000003')||'|'||(SELECT count(*) FROM supplier_settlement_policy WHERE supplier_id='22000000-0000-4000-8000-000000000003' AND enabled=false)||'|'||(SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('supplier_payable_accrual','supplier_settlement_batch','supplier_payout_attempt','supplier_appeal'))").Trim()
    if ($upgrade -ne "1|1|1|4") { throw "The V21 fixture was not upgraded additively or the existing supplier was not safely disabled." }
    Write-Host "PASS populated V21 database upgraded to V22 with the existing supplier and disabled payout policy preserved"

    & $GoExecutable test ./internal/store -run TestSupplierSettlementPlatformUsageConcurrencyAndPayoutIntegration -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw "Supplier settlement integration test failed." }
    Write-Host "PASS platform usage concurrency, declaration isolation, disputes, bill matching, refund share, retry, payout, ledger, and reconciliation integration"
} finally {
    if ($created -and $database -match '^relayedock_settlement_test_[0-9a-f]{16}$') {
        docker exec $postgres dropdb --no-password --if-exists --force -U $settings.POSTGRES_USER --maintenance-db $settings.POSTGRES_DB $database | Out-Null
    }
    if ($null -eq $oldTestURL) { Remove-Item Env:TEST_DATABASE_URL -ErrorAction SilentlyContinue } else { $env:TEST_DATABASE_URL = $oldTestURL }
    if ($null -eq $oldDatabaseURL) { Remove-Item Env:DATABASE_URL -ErrorAction SilentlyContinue } else { $env:DATABASE_URL = $oldDatabaseURL }
}
