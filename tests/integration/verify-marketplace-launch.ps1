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
foreach ($required in @("POSTGRES_USER", "POSTGRES_DB", "DATABASE_URL", "POSTGRES_HOST_PORT")) {
    if (-not $settings.ContainsKey($required) -or [string]::IsNullOrWhiteSpace($settings[$required])) { throw "Missing $required." }
}
$postgresHostPort = 0
if (-not [int]::TryParse($settings.POSTGRES_HOST_PORT, [ref]$postgresHostPort) -or $postgresHostPort -lt 1 -or $postgresHostPort -gt 65535) {
    throw "POSTGRES_HOST_PORT must be a valid TCP port."
}
$postgres = @(docker ps --filter "ancestor=postgres:17-alpine" --format "{{.ID}}") | Select-Object -First 1
if ([string]::IsNullOrWhiteSpace($postgres)) { throw "The local RelayDock PostgreSQL container is not running." }
$database = "relayedock_marketplace_test_" + [Guid]::NewGuid().ToString("N").Substring(0, 16)
if ($database -notmatch '^relayedock_marketplace_test_[0-9a-f]{16}$') { throw "Unsafe generated database name." }
$created = $false
$oldTestURL = $env:TEST_DATABASE_URL
$oldDatabaseURL = $env:DATABASE_URL

function Invoke-Psql([string]$Sql) {
    $result = docker exec $postgres psql --no-psqlrc --no-align --tuples-only --quiet --no-password --set=ON_ERROR_STOP=1 -U $settings.POSTGRES_USER -d $database -c $Sql 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Disposable Marketplace verification failed; PostgreSQL diagnostics were suppressed." }
    return [string]::Join("`n", @($result))
}

try {
    docker exec $postgres createdb --no-password -U $settings.POSTGRES_USER --maintenance-db $settings.POSTGRES_DB $database | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not create the disposable database." }
    $created = $true

    Invoke-Psql "CREATE TABLE schema_migrations(version bigint PRIMARY KEY,name text NOT NULL,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now());" | Out-Null
    foreach ($version in 1..22) {
        $file = Get-ChildItem (Join-Path $repo "migrations") -Filter ("{0:D4}_*.sql" -f $version) -File
        if (@($file).Count -ne 1) { throw "Migration $version was not found exactly once." }
        $savedPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "Continue"
            Get-Content -Raw -LiteralPath $file.FullName | docker exec -i $postgres psql --no-psqlrc --quiet --no-password --set=ON_ERROR_STOP=1 -U $settings.POSTGRES_USER -d $database 2>&1 | Out-Null
            $migrationExit = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $savedPreference
        }
        if ($migrationExit -ne 0) { throw "Applying the V22 upgrade fixture failed at migration $version; diagnostics were suppressed." }
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash.ToLowerInvariant()
        $name = $file.BaseName.Substring(5)
        Invoke-Psql "INSERT INTO schema_migrations(version,name,checksum) VALUES($version,'$name','$hash');" | Out-Null
    }
    Invoke-Psql "INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at) VALUES('23000000-0000-4000-8000-000000000001','marketplace-upgrade@example.invalid','synthetic-hash','Synthetic Upgrade Admin','ADMIN','ACTIVE',now()); INSERT INTO organizations(id,name,slug,status,billing_region,metadata) VALUES('23000000-0000-4000-8000-000000000002','Synthetic Upgrade Marketplace','synthetic-upgrade-marketplace','ACTIVE','US','{}'); BEGIN; SELECT set_config('relaydock.supplier_admin_action','true',true); INSERT INTO supplier_organizations(id,organization_id,owner_user_id,legal_name,display_name,registration_number,incorporation_country,kyb_status,contract_status,status,payout_currency) VALUES('23000000-0000-4000-8000-000000000003','23000000-0000-4000-8000-000000000002','23000000-0000-4000-8000-000000000001','Synthetic Upgrade Marketplace','Synthetic Upgrade Marketplace','SYNTHETIC-UPGRADE','US','VERIFIED','ACTIVE','APPROVED','USD'); COMMIT; UPDATE provider_quality_policies SET enabled=true WHERE provider_id=(SELECT id FROM providers WHERE slug='openai'); INSERT INTO supplier_provider_links(provider_id,supplier_id,status,linked_by,reason) VALUES((SELECT id FROM providers WHERE slug='openai'),'23000000-0000-4000-8000-000000000003','ACTIVE','23000000-0000-4000-8000-000000000001','synthetic pre-v23 link'); INSERT INTO provider_marketplace_listings(id,provider_id,endpoint,supported_models,price,status,uptime,verified) VALUES('23000000-0000-4000-8000-000000000004',(SELECT id FROM providers WHERE slug='openai'),'https://upgrade-provider.example.invalid/v1',jsonb_build_array('synthetic-model'),jsonb_build_object('declared','1.0'),'ACTIVE',100,true);" | Out-Null

    $uri = [Uri]$settings.DATABASE_URL
    $builder = [UriBuilder]$uri
    $builder.Host = "127.0.0.1"
    $builder.Port = $postgresHostPort
    $builder.Path = "/$database"
    $env:DATABASE_URL = $builder.Uri.AbsoluteUri
    $env:TEST_DATABASE_URL = $builder.Uri.AbsoluteUri
    & $GoExecutable run ./cmd/relayedock migrate
    if ($LASTEXITCODE -ne 0) { throw "The V22 through V25 application migration failed." }

    $upgrade = (Invoke-Psql "SELECT (SELECT max(version) FROM schema_migrations)||'|'||(SELECT count(*) FROM schema_migrations WHERE version=23 AND name='marketplace_launch_acceptance')||'|'||(SELECT status FROM provider_marketplace_listings WHERE id='23000000-0000-4000-8000-000000000004')||'|'||(SELECT contract_status||':'||tax_status||':'||payment_status||':'||security_status||':'||production_payout_enabled FROM supplier_payout_readiness_review WHERE supplier_id='23000000-0000-4000-8000-000000000003')||'|'||(SELECT count(*) FROM marketplace_launch_review);").Trim()
    if ($upgrade -ne "25|1|ACTIVE|PENDING:PENDING:PENDING:PENDING:false|0") { throw "The V22 fixture was not upgraded through V25, upgraded additively, and kept fail-closed." }
    Write-Host "PASS populated V22 Marketplace upgraded through V25; declarations preserved and payout readiness defaults blocked"

    & $GoExecutable test ./internal/store -run TestSupplierOnboardingIntegration -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw "Supplier registration and qualification integration test failed." }
    Write-Host "PASS supplier registration, self-approval isolation, endpoint verification, qualification review, suspension/exit request concurrency"

    & $GoExecutable test ./internal/store -run TestMarketplaceLaunchAcceptanceAndLifecycleIntegration -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw "Marketplace launch integration test failed." }
    Write-Host "PASS registration-to-exit acceptance, canary routing, exact ledger evidence, payout gate, concurrency, and lifecycle integration"
} finally {
    if ($created -and $database -match '^relayedock_marketplace_test_[0-9a-f]{16}$') {
        docker exec $postgres dropdb --no-password --if-exists --force -U $settings.POSTGRES_USER --maintenance-db $settings.POSTGRES_DB $database | Out-Null
    }
    if ($null -eq $oldTestURL) { Remove-Item Env:TEST_DATABASE_URL -ErrorAction SilentlyContinue } else { $env:TEST_DATABASE_URL = $oldTestURL }
    if ($null -eq $oldDatabaseURL) { Remove-Item Env:DATABASE_URL -ErrorAction SilentlyContinue } else { $env:DATABASE_URL = $oldDatabaseURL }
}
