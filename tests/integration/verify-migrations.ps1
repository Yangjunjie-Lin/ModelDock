[CmdletBinding()]
param(
    [string]$EnvFile = "",
    [ValidateRange(10, 120)]
    [int]$StartupTimeoutSeconds = 45,
    [switch]$ConfirmIsolatedTestDatabase
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

if (-not $ConfirmIsolatedTestDatabase) {
    throw "Pass -ConfirmIsolatedTestDatabase only after confirming this is a disposable local Docker test run."
}

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
if ([string]::IsNullOrWhiteSpace($EnvFile)) {
    $EnvFile = Join-Path $repoRoot ".env"
} elseif (-not [System.IO.Path]::IsPathRooted($EnvFile)) {
    $EnvFile = Join-Path $repoRoot $EnvFile
}
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) {
    throw "The requested Docker environment file does not exist."
}
$EnvFile = (Resolve-Path -LiteralPath $EnvFile).Path

$postgresContainer = "relaydock-postgres-1"
$internalNetwork = "relaydock_relaydock-internal"
$serverImage = "relaydock/server:local"
$runID = [Guid]::NewGuid().ToString("N").Substring(0, 20)
$testDatabase = "relaydock_migration_test_$runID"
$expectedLedger = @(
    "1:core",
    "2:v2",
    "3:v2_statuses",
    "4:project_route_soft_delete",
    "5:openai_compatible_providers",
    "6:modeldock",
    "7:accounts",
    "8:pricing",
    "9:funding_ledger",
    "10:payment_orders",
    "11:subscriptions",
    "12:financial_close",
    "13:financial_close_hardening",
    "14:provider_commercial_governance",
    "15:provider_pricing_hardening",
	"16:public_operations_governance",
	"17:observability_support",
	"18:beta_runtime_hardening",
	"19:public_commercial_onboarding",
	"20:supplier_onboarding",
	"21:provider_quality",
	"22:supplier_settlement",
	"23:marketplace_launch_acceptance",
	"24:exact_money_and_release_evidence",
	"25:commercial_attestation_and_decimal_hardening"
)
$expectedProviderSeeds = @(
    "anthropic|anthropic|https://api.anthropic.com/v1",
    "deepseek|deepseek|https://api.deepseek.com/v1",
    "gemini|gemini|https://generativelanguage.googleapis.com/v1beta/openai",
    "glm|glm|https://open.bigmodel.cn/api/paas/v4",
    "kimi|kimi|https://api.moonshot.cn/v1",
    "openai|openai|https://api.openai.com/v1",
    "openrouter|openrouter|https://openrouter.ai/api/v1",
    "qwen|qwen|https://dashscope.aliyuncs.com/compatible-mode/v1"
)

$dockerExecutable = $null
$postgresUser = $null
$postgresDatabase = $null
$databaseCreated = $false
$testDatabaseURL = $null
$originalDatabaseURLExists = Test-Path Env:DATABASE_URL
$originalDatabaseURL = $env:DATABASE_URL
$createdContainers = New-Object 'System.Collections.Generic.List[string]'

function Invoke-DockerRaw {
    param([string[]]$Arguments)

    $savedErrorActionPreference = $ErrorActionPreference
    try {
        # Native stderr is captured because Docker may include environment or
        # connection details in diagnostics. Callers receive it for matching,
        # but this runner never echoes it.
        $ErrorActionPreference = "Continue"
        $outputLines = @(& $script:dockerExecutable @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    } catch {
        throw "Docker could not be invoked. Diagnostic output was suppressed."
    } finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }

    [pscustomobject]@{
        ExitCode = [int]$exitCode
        Output = [string]::Join([Environment]::NewLine, @($outputLines | ForEach-Object { [string]$_ }))
    }
}

function Invoke-DockerChecked {
    param(
        [string[]]$Arguments,
        [string]$Operation
    )

    $result = Invoke-DockerRaw -Arguments $Arguments
    if ($result.ExitCode -ne 0) {
        throw "$Operation failed (Docker exit code $($result.ExitCode)); diagnostic output was suppressed."
    }
    return $result
}

function ConvertFrom-DockerJson {
    param(
        [string]$Json,
        [string]$Operation
    )

    try {
        return @($Json | ConvertFrom-Json)
    } catch {
        throw "$Operation returned invalid JSON; diagnostic output was suppressed."
    }
}

function Test-LocalDockerEndpoint {
    param([string]$Endpoint)

    if ([string]::IsNullOrWhiteSpace($Endpoint)) { return $false }
    if ($Endpoint -match '^(npipe|unix)://') { return $true }
    if ($Endpoint -notmatch '^tcp://') { return $false }

    try {
        $uri = [Uri]($Endpoint -replace '^tcp://', 'http://')
    } catch {
        return $false
    }
    return $uri.Host -in @("127.0.0.1", "localhost", "::1")
}

function Read-DotEnv {
    param([string]$Path)

    $values = @{}
    foreach ($rawLine in Get-Content -LiteralPath $Path) {
        $line = ([string]$rawLine).Trim()
        if ($line.Length -eq 0 -or $line.StartsWith("#")) { continue }
        if ($line.StartsWith("export ")) { $line = $line.Substring(7).TrimStart() }
        $separator = $line.IndexOf("=")
        if ($separator -lt 1) { continue }
        $name = $line.Substring(0, $separator).Trim()
        if ($name -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') { continue }
        $value = $line.Substring($separator + 1).Trim()
        if ($value.Length -ge 2) {
            $first = $value.Substring(0, 1)
            $last = $value.Substring($value.Length - 1, 1)
            if (($first -eq '"' -and $last -eq '"') -or ($first -eq "'" -and $last -eq "'")) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }
        $values[$name] = $value
    }
    return $values
}

function Get-ContainerInspect {
    param([string]$Name)

    $result = Invoke-DockerChecked -Arguments @("container", "inspect", $Name) -Operation "Inspecting a test container"
    $items = @(ConvertFrom-DockerJson -Json $result.Output -Operation "Container inspection")
    if ($items.Count -ne 1) { throw "Container inspection returned an unexpected result count." }
    return $items[0]
}

function Assert-TestContainerIsolation {
    param([string]$Name)

    $container = Get-ContainerInspect -Name $Name
    if ([string]$container.Config.Image -ne $script:serverImage) {
        throw "The migration test container is not using the required local RelayDock image."
    }
    if ([string]$container.Config.Labels.'com.relaydock.integration' -ne "migration-contract" -or
        [string]$container.Config.Labels.'com.relaydock.integration-run' -ne $script:runID) {
        throw "The migration test container does not carry this run's ownership labels."
    }
    if ([bool]$container.HostConfig.PublishAllPorts) {
        throw "The migration test container unexpectedly publishes ports."
    }
    if ($null -ne $container.HostConfig.PortBindings) {
        foreach ($binding in $container.HostConfig.PortBindings.PSObject.Properties) {
            if ($null -ne $binding.Value -and @($binding.Value).Count -gt 0) {
                throw "The migration test container unexpectedly has a host port binding."
            }
        }
    }
    $attachedNetworks = @($container.NetworkSettings.Networks.PSObject.Properties | ForEach-Object { $_.Name })
    if ($attachedNetworks.Count -ne 1 -or $attachedNetworks[0] -ne $script:internalNetwork) {
        throw "The migration test container is not isolated to the RelayDock internal network."
    }
}

function Get-ContainerState {
    param([string]$Name)

    $result = Invoke-DockerRaw -Arguments @("container", "inspect", "--format", "{{.State.Status}}|{{.State.ExitCode}}", $Name)
    if ($result.ExitCode -ne 0) { return $null }
    $parts = $result.Output.Trim().Split("|")
    if ($parts.Count -ne 2) { return $null }
    return [pscustomobject]@{ Status = $parts[0]; ExitCode = [int]$parts[1] }
}

function Start-TestServer {
    param([string]$Name)

    $existing = Invoke-DockerRaw -Arguments @("container", "inspect", $Name)
    if ($existing.ExitCode -eq 0) {
        throw "A container already exists with a generated migration test name; it was not modified."
    }
    $arguments = @(
        "run", "--detach",
        "--name", $Name,
        "--label", "com.relaydock.integration=migration-contract",
        "--label", "com.relaydock.integration-run=$($script:runID)",
        "--network", $script:internalNetwork,
        "--read-only",
        "--tmpfs", "/tmp:size=64m,mode=1777",
        "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges:true",
        "--env-file", $script:EnvFile,
        "--env", "DATABASE_URL",
        "--env", "LOG_DIR=",
        $script:serverImage
    )
    $runResult = Invoke-DockerRaw -Arguments $arguments
    if ($runResult.ExitCode -ne 0) {
        # A failed `docker run` can occasionally leave a created container.
        # Track it only if its unguessable run label proves ownership.
        $candidate = Invoke-DockerRaw -Arguments @("container", "inspect", $Name)
        if ($candidate.ExitCode -eq 0) {
            $candidateItems = @(ConvertFrom-DockerJson -Json $candidate.Output -Operation "Failed test-container inspection")
            if ($candidateItems.Count -eq 1 -and
                [string]$candidateItems[0].Config.Labels.'com.relaydock.integration' -eq "migration-contract" -and
                [string]$candidateItems[0].Config.Labels.'com.relaydock.integration-run' -eq $script:runID) {
                [void]$script:createdContainers.Add($Name)
            }
        }
        throw "Starting an isolated migration test container failed; diagnostic output was suppressed."
    }
    [void]$script:createdContainers.Add($Name)
    Assert-TestContainerIsolation -Name $Name
}

function Remove-TestContainer {
    param(
        [string]$Name,
        [switch]$BestEffort
    )

    $inspectResult = Invoke-DockerRaw -Arguments @("container", "inspect", $Name)
    if ($inspectResult.ExitCode -ne 0) { return }
    $items = @(ConvertFrom-DockerJson -Json $inspectResult.Output -Operation "Cleanup container inspection")
    if ($items.Count -ne 1 -or
        [string]$items[0].Config.Labels.'com.relaydock.integration' -ne "migration-contract" -or
        [string]$items[0].Config.Labels.'com.relaydock.integration-run' -ne $script:runID) {
        if ($BestEffort) { return }
        throw "Container cleanup was refused because the ownership label did not match this test run."
    }

    $result = Invoke-DockerRaw -Arguments @("container", "rm", "--force", $Name)
    if (-not $BestEffort -and $result.ExitCode -ne 0) {
        throw "Removing an isolated migration test container failed; diagnostic output was suppressed."
    }
}

function Invoke-PsqlRaw {
    param(
        [string]$Database,
        [string]$Sql
    )

    Invoke-DockerRaw -Arguments @(
        "container", "exec", $script:postgresContainer,
        "psql", "--no-psqlrc", "--no-align", "--tuples-only", "--quiet",
        "--no-password", "--set=ON_ERROR_STOP=1",
        "--username", $script:postgresUser,
        "--dbname", $Database,
        "--command", $Sql
    )
}

function Invoke-PsqlChecked {
    param(
        [string]$Database,
        [string]$Sql,
        [string]$Operation
    )

    $result = Invoke-PsqlRaw -Database $Database -Sql $Sql
    if ($result.ExitCode -ne 0) {
        throw "$Operation failed; database diagnostic output was suppressed."
    }
    return $result
}

function Recreate-TestDatabase {
    $dropResult = Invoke-DockerRaw -Arguments @(
        "container", "exec", $script:postgresContainer,
        "dropdb", "--no-password", "--if-exists", "--force",
        "--username", $script:postgresUser,
        "--maintenance-db", $script:postgresDatabase,
        $script:testDatabase
    )
    if ($dropResult.ExitCode -ne 0) {
        throw "Recreating the disposable migration database failed during drop; diagnostic output was suppressed."
    }
    $createResult = Invoke-DockerRaw -Arguments @(
        "container", "exec", $script:postgresContainer,
        "createdb", "--no-password", "--username", $script:postgresUser,
        "--maintenance-db", $script:postgresDatabase,
        $script:testDatabase
    )
    if ($createResult.ExitCode -ne 0) {
        throw "Recreating the disposable migration database failed during create; diagnostic output was suppressed."
    }
}

function Get-SHA256Hex {
    param([byte[]]$Bytes)

    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return -join ($sha.ComputeHash($Bytes) | ForEach-Object { $_.ToString("x2") })
    } finally {
        $sha.Dispose()
    }
}

function Initialize-PopulatedV1Database {
    $migrationPath = Join-Path $script:repoRoot "migrations\0001_core.sql"
    $migrationBytes = [System.IO.File]::ReadAllBytes($migrationPath)
    $migrationSQL = [System.Text.Encoding]::UTF8.GetString($migrationBytes)
    $checksum = Get-SHA256Hex -Bytes $migrationBytes
    Invoke-PsqlChecked -Database $script:testDatabase -Sql $migrationSQL -Operation "Applying the V1 schema fixture" | Out-Null
    $ledgerSQL = @"
CREATE TABLE schema_migrations (
  version bigint PRIMARY KEY,
  name text NOT NULL,
  checksum text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO schema_migrations(version,name,checksum) VALUES (1,'core','$checksum');
"@
    Invoke-PsqlChecked -Database $script:testDatabase -Operation "Recording the V1 migration fixture" -Sql $ledgerSQL | Out-Null
    $fixtureSQL = @"
INSERT INTO users(id,email,password_hash,display_name,role,status)
VALUES ('11111111-1111-4111-8111-111111111111','migration-v1@example.invalid','synthetic-v1-hash','Synthetic V1 User','USER','ACTIVE');
INSERT INTO api_keys(id,user_id,name,environment,key_prefix,key_hash,status,allowed_models)
VALUES (
  '22222222-2222-4222-8222-222222222222',
  '11111111-1111-4111-8111-111111111111',
  'Synthetic V1 key','test','rdk_test_v1_fixture',
  decode('00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff','hex'),
  'ACTIVE','[]'::jsonb
);
INSERT INTO usage_daily(date,user_id,api_key_id,model,requests,input_tokens,output_tokens,cost)
VALUES (
  DATE '2026-01-02',
  '11111111-1111-4111-8111-111111111111',
  '22222222-2222-4222-8222-222222222222',
  'synthetic-v1-model',1,10,5,0.00012345
);
"@
    Invoke-PsqlChecked -Database $script:testDatabase -Operation "Seeding populated V1 data" -Sql $fixtureSQL | Out-Null
}

function Initialize-PopulatedV12FinancialDatabase {
    $ledgerValues = New-Object 'System.Collections.Generic.List[string]'
    $migrationNames = @(
        "core", "v2", "v2_statuses", "project_route_soft_delete", "openai_compatible_providers", "modeldock",
        "accounts", "pricing", "funding_ledger", "payment_orders", "subscriptions", "financial_close"
    )
    for ($index = 1; $index -le 12; $index++) {
        $path = Join-Path $script:repoRoot ("migrations\{0:D4}_{1}.sql" -f $index, $migrationNames[$index - 1])
        $bytes = [System.IO.File]::ReadAllBytes($path)
        $migrationSqlText = [System.Text.Encoding]::UTF8.GetString($bytes)
        $savedErrorActionPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "Continue"
            $migrationSqlText | & $script:dockerExecutable container exec -i $script:postgresContainer `
                psql --no-psqlrc --quiet --no-password --set=ON_ERROR_STOP=1 --username $script:postgresUser --dbname $script:testDatabase 2>&1 | Out-Null
            $applyExitCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $savedErrorActionPreference
        }
        if ($applyExitCode -ne 0) {
            throw ("Applying the V12 fixture migration {0} failed; database diagnostic output was suppressed." -f $index)
        }
        $ledgerValues.Add(("({0},'{1}','{2}')" -f $index, $migrationNames[$index - 1], (Get-SHA256Hex -Bytes $bytes)))
    }
    $ledgerSQL = @"
CREATE TABLE schema_migrations (
  version bigint PRIMARY KEY,
  name text NOT NULL,
  checksum text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO schema_migrations(version,name,checksum) VALUES $([string]::Join(',', $ledgerValues));
"@
    Invoke-PsqlChecked -Database $script:testDatabase -Sql $ledgerSQL -Operation "Recording the V12 migration fixture" | Out-Null
    Invoke-PsqlChecked -Database $script:testDatabase -Operation "Seeding active V12 financial evidence" -Sql @"
UPDATE wallets SET billing_mode='PREPAID',available_balance=4,reserved_balance=9,credit_limit=0,status='ACTIVE'
WHERE organization_id='00000000-0000-4000-8000-000000000001';
INSERT INTO wallet_cash_lot(id,wallet_id,source_kind,source_reference,original_amount,remaining_amount,currency,refundable,created_at)
SELECT '13100000-0000-4000-8000-000000000001',id,'OPENING','v12:available-cash',1,1,currency,false,
 (SELECT applied_at+interval '3 seconds' FROM schema_migrations WHERE version=12)
FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001';
INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,request_fingerprint,status,
 currency,maximum_amount,estimated_input_tokens,max_output_tokens,reserved_at)
SELECT '13100000-0000-4000-8000-000000000002',id,organization_id,'00000000-0000-4000-8000-000000000002',
 'v12-active-reservation','v12-active-reservation','v12-active-reservation','RESERVED',currency,8,0,0,
 (SELECT applied_at-interval '1 second' FROM schema_migrations WHERE version=12)
FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001';
INSERT INTO wallet_cash_lot(id,wallet_id,source_kind,source_reference,original_amount,remaining_amount,currency,refundable,created_at)
SELECT '13100000-0000-4000-8000-000000000006',id,'ADJUSTMENT','v12:post-migration-cash-a',0.4,0.4,currency,false,
 (SELECT applied_at+interval '1 second' FROM schema_migrations WHERE version=12)
FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001';
INSERT INTO wallet_cash_lot(id,wallet_id,source_kind,source_reference,original_amount,remaining_amount,currency,refundable,created_at)
SELECT '13100000-0000-4000-8000-000000000009',id,'ADJUSTMENT','v12:post-migration-cash-b',0.6,0.6,currency,false,
 (SELECT applied_at+interval '2 seconds' FROM schema_migrations WHERE version=12)
FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001';
INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,request_fingerprint,status,
 currency,maximum_amount,estimated_input_tokens,max_output_tokens,reserved_at)
SELECT '13100000-0000-4000-8000-000000000007',id,organization_id,'00000000-0000-4000-8000-000000000002',
 'v12-post-migration-reservation-a','v12-post-migration-reservation-a','v12-post-migration-reservation-a','RESERVED',currency,0.75,0,0,
 (SELECT applied_at+interval '4 seconds' FROM schema_migrations WHERE version=12)
FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001';
INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,request_fingerprint,status,
 currency,maximum_amount,estimated_input_tokens,max_output_tokens,reserved_at)
SELECT '13100000-0000-4000-8000-000000000008',id,organization_id,'00000000-0000-4000-8000-000000000002',
 'v12-post-migration-reservation-b','v12-post-migration-reservation-b','v12-post-migration-reservation-b','RESERVED',currency,0.25,0,0,
 (SELECT applied_at+interval '5 seconds' FROM schema_migrations WHERE version=12)
FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001';

INSERT INTO recharge_order(id,platform_order_no,organization_id,wallet_id,payment_provider,provider_order_no,status,amount,currency,
 region,idempotency_key,request_fingerprint,wallet_transaction_id,ledger_journal_id,expires_at,paid_at,credited_at)
SELECT '13100000-0000-4000-8000-000000000003','V12-REFUND-SOURCE',organization_id,id,'sandbox','v12-provider-order',
 'PENDING',3,currency,'CN','v12-refund-source','v12-refund-source',NULL,NULL,now()+interval '1 day',NULL,NULL
FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001';
INSERT INTO wallet_cash_lot(id,wallet_id,recharge_order_id,source_kind,source_reference,original_amount,remaining_amount,currency,refundable,created_at)
SELECT '13100000-0000-4000-8000-000000000004',wallet_id,id,'RECHARGE',platform_order_no,3,3,currency,true,
 (SELECT applied_at+interval '6 seconds' FROM schema_migrations WHERE version=12)
FROM recharge_order WHERE id='13100000-0000-4000-8000-000000000003';
INSERT INTO refund_order(id,platform_refund_no,recharge_order_id,payment_provider,provider_refund_no,status,amount,currency,reason,idempotency_key)
VALUES('13100000-0000-4000-8000-000000000005','V12-PENDING-REFUND','13100000-0000-4000-8000-000000000003',
 'sandbox','v12-provider-refund','PENDING',2,'USD','synthetic migration refund','v12-pending-refund');
"@ | Out-Null
}

function Assert-PopulatedV12FinancialUpgrade {
    $result = Invoke-PsqlChecked -Database $script:testDatabase -Operation "Validating active V12 financial evidence after upgrade" -Sql @"
SELECT concat_ws('|',
 (SELECT reserved_amount::text FROM funding_cash_allocation WHERE operation_id='13100000-0000-4000-8000-000000000002'),
 (SELECT remaining_amount::text FROM wallet_cash_lot WHERE source_reference='migration:0013:reserved:'||wallet_id::text),
 (SELECT sum(reserved_amount)::text FROM funding_cash_allocation WHERE operation_id='13100000-0000-4000-8000-000000000007'),
 (SELECT sum(reserved_amount)::text FROM funding_cash_allocation WHERE operation_id='13100000-0000-4000-8000-000000000008'),
 (SELECT sum(remaining_amount)::text FROM wallet_cash_lot
   WHERE id IN ('13100000-0000-4000-8000-000000000006','13100000-0000-4000-8000-000000000009')),
 (SELECT sum(allocation.reserved_amount)::text FROM funding_cash_allocation allocation
   JOIN funding_operation operation ON operation.id=allocation.operation_id
   WHERE operation.wallet_id=(SELECT id FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001')),
 (SELECT reserved_amount::text FROM refund_cash_allocation WHERE refund_order_id='13100000-0000-4000-8000-000000000005'),
 (SELECT remaining_amount::text FROM wallet_cash_lot WHERE id='13100000-0000-4000-8000-000000000004'),
 (SELECT COALESCE(sum(remaining_amount),0)::text FROM wallet_cash_lot lot JOIN wallets wallet ON wallet.id=lot.wallet_id
   WHERE wallet.organization_id='00000000-0000-4000-8000-000000000001' AND lot.refundable));
"@
    if ($result.Output.Trim() -ne "8.000000000000|0.000000000000|0.750000000000|0.250000000000|0.000000000000|9.000000000000|2.000000000000|1.000000000000|1.000000000000") {
        throw "Schema 13 did not preserve active funding/refund holds from the V12 fixture."
    }
}

function Assert-PopulatedV1Upgrade {
    $result = Invoke-PsqlChecked -Database $script:testDatabase -Operation "Validating populated V1 data after upgrade" -Sql @"
SELECT concat_ws('|',
  (SELECT count(*) FROM users WHERE id='11111111-1111-4111-8111-111111111111'),
  (SELECT count(*) FROM api_keys WHERE id='22222222-2222-4222-8222-222222222222'
    AND organization_id='00000000-0000-4000-8000-000000000001'
    AND project_id='00000000-0000-4000-8000-000000000002'),
  (SELECT count(*) FROM api_key_versions WHERE api_key_id='22222222-2222-4222-8222-222222222222'
    AND version=1 AND status='ACTIVE'),
  (SELECT count(*) FROM organization_memberships WHERE organization_id='00000000-0000-4000-8000-000000000001'
    AND user_id='11111111-1111-4111-8111-111111111111' AND status='ACTIVE'),
  (SELECT count(*) FROM project_memberships WHERE project_id='00000000-0000-4000-8000-000000000002'
    AND user_id='11111111-1111-4111-8111-111111111111' AND status='ACTIVE'),
  (SELECT count(*) FROM usage_daily WHERE api_key_id='22222222-2222-4222-8222-222222222222'
    AND organization_id='00000000-0000-4000-8000-000000000001'
    AND project_id='00000000-0000-4000-8000-000000000002' AND cost=0.00012345),
  (SELECT count(*) FROM wallets WHERE organization_id='00000000-0000-4000-8000-000000000001')
);
"@
    if ($result.Output.Trim() -ne "1|1|1|1|1|1|1") {
        throw "The populated V1 fixture was not preserved and scoped exactly once during upgrade."
    }

    $compatibilityResult = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT count(*) FROM organization_subscription subscription JOIN plan_version version ON version.id=subscription.plan_version_id JOIN subscription_plan plan ON plan.id=version.subscription_plan_id WHERE subscription.organization_id='00000000-0000-4000-8000-000000000001' AND subscription.status='ACTIVE' AND plan.slug='legacy-compat' AND version.token_billing_mode='METERED_SEPARATE'" `
        -Operation "Validating legacy organization subscription compatibility"
    if ($compatibilityResult.Output.Trim() -ne "1") {
        throw "The populated V1 organization did not receive the finite metered compatibility subscription."
    }
}

function Get-NonEmptyLines {
    param([string]$Text)

    @($Text -split "`r?`n" | ForEach-Object { $_.Trim() } | Where-Object { $_.Length -gt 0 })
}

function Assert-ExpectedLedger {
    $result = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT version::text || ':' || name FROM schema_migrations ORDER BY version" `
        -Operation "Reading the migration ledger"
    $actual = @(Get-NonEmptyLines -Text $result.Output)
    $actualText = [string]::Join("`n", $actual)
    $expectedText = [string]::Join("`n", $script:expectedLedger)
    if ($actualText -ne $expectedText) {
        throw "The migration ledger does not exactly match the expected V2 migration manifest."
    }

    $checksumResult = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT count(*) FROM schema_migrations WHERE checksum !~ '^[0-9a-f]{64}$'" `
        -Operation "Validating migration checksums"
    if ($checksumResult.Output.Trim() -ne "0") {
        throw "The migration ledger contains an invalid checksum."
    }

    $providerResult = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT slug || '|' || provider_type || '|' || base_url FROM providers WHERE slug IN ('anthropic','openai','deepseek','gemini','glm','kimi','openrouter','qwen') ORDER BY slug" `
        -Operation "Validating OpenAI-compatible provider seeds"
    $actualProviders = [string]::Join("`n", @(Get-NonEmptyLines -Text $providerResult.Output))
    $expectedProviders = [string]::Join("`n", $script:expectedProviderSeeds)
    if ($actualProviders -ne $expectedProviders) {
        throw "The OpenAI-compatible provider seed set does not match the production manifest."
    }

    $pricingResult = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('provider_cost_price_book','customer_retail_price_book','organization_price_plan','model_price_version','pricing_quote','usage_price_snapshot','promotion_credit')" `
        -Operation "Validating commercial pricing tables"
    if ($pricingResult.Output.Trim() -ne "7") {
        throw "The commercial pricing migration did not create every required pricing table."
    }

    $fundingResult = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('ledger_account','ledger_journal','ledger_journal_entry','funding_operation','funding_operation_event','funding_provider_attempt','funding_usage_adjustment')" `
        -Operation "Validating funding ledger tables"
    if ($fundingResult.Output.Trim() -ne "7") {
        throw "The funding ledger migration did not create every required table."
    }

    $paymentResult = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('recharge_order','payment_attempt','payment_webhook_event','refund_order','payment_reconciliation_record')" `
        -Operation "Validating payment order tables"
    if ($paymentResult.Output.Trim() -ne "5") {
        throw "The payment order migration did not create every required table."
    }

    $subscriptionResult = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('subscription_plan','plan_version','plan_entitlement','organization_subscription','subscription_invoice','subscription_event','trial','coupon')" `
        -Operation "Validating subscription tables"
    if ($subscriptionResult.Output.Trim() -ne "8") {
        throw "The subscription migration did not create every required table."
    }

    $subscriptionSeedResult = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT string_agg(slug,',' ORDER BY slug) FROM subscription_plan WHERE slug IN ('free','developer','team','enterprise')" `
        -Operation "Validating subscription templates"
    if ($subscriptionSeedResult.Output.Trim() -ne "developer,enterprise,free,team") {
        throw "The default subscription template set is incomplete."
    }

	$hardeningResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('funding_cash_allocation','refund_cash_allocation','invoice_export_batch')" `
		-Operation "Validating financial close hardening tables"
	if ($hardeningResult.Output.Trim() -ne "3") {
		throw "The financial close hardening migration did not create every required table."
	}
	$hardeningLinkResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND (table_name,column_name) IN (('invoice_application','invoice_export_batch_id'),('provider_statement','import_fingerprint_sha256'),('ledger_journal','provider_statement_id'))" `
		-Operation "Validating financial close hardening evidence links"
	if ($hardeningLinkResult.Output.Trim() -ne "3") {
		throw "The financial close hardening migration did not create every required evidence link."
	}

	$observabilityResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('status_events','observability_slos','support_tickets','support_ticket_messages')" `
		-Operation "Validating observability and support tables"
	if ($observabilityResult.Output.Trim() -ne "4") {
		throw "The observability migration did not create every required table."
	}
	$observabilityColumnResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND (table_name,column_name) IN (('request_logs','trace_id'),('alerts','dedupe_key'),('alerts','details'),('alerts','resolved_at'),('alerts','last_seen_at'))" `
		-Operation "Validating observability evidence columns"
	if ($observabilityColumnResult.Output.Trim() -ne "5") {
		throw "The observability migration did not create every required evidence column."
	}
	$sloResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM observability_slos WHERE name IN ('gateway_availability','control_plane_availability','payment_webhook_processing','ledger_settlement_latency','provider_routing_success')" `
		-Operation "Validating required SLO definitions"
	if ($sloResult.Output.Trim() -ne "5") {
		throw "The observability migration did not seed every required SLO."
	}

	$commercialOnboardingResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('public_commercial_terms','public_payment_fee_schedule','commercial_funnel_events','commercial_funnel_api_call_counter')" `
		-Operation "Validating public commercial onboarding tables"
	if ($commercialOnboardingResult.Output.Trim() -ne "4") {
		throw "The public commercial onboarding migration did not create every required table."
	}
	$commercialOnboardingTriggerResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN ('public_commercial_terms_immutable_trigger','public_payment_fee_schedule_immutable_trigger','commercial_funnel_events_immutable_trigger','commercial_funnel_user_verification','commercial_funnel_api_key_insert','commercial_funnel_recharge','commercial_funnel_request_log_insert','commercial_funnel_subscription_event_insert')" `
		-Operation "Validating public commercial onboarding triggers"
	if ($commercialOnboardingTriggerResult.Output.Trim() -ne "8") {
		throw "The public commercial onboarding migration did not create every required evidence trigger."
	}

	$supplierResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('supplier_organizations','supplier_contacts','supplier_endpoints','supplier_credentials','supplier_data_residency_declarations','supplier_security_questionnaires','supplier_model_applications','supplier_price_applications','supplier_reviews','supplier_status_events')" `
		-Operation "Validating supplier onboarding tables"
	if ($supplierResult.Output.Trim() -ne "10") {
		throw "The supplier onboarding migration did not create every required table."
	}
	$supplierTriggerResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN ('supplier_status_protection_trigger','supplier_review_append_only_trigger')" `
		-Operation "Validating supplier onboarding triggers"
	if ($supplierTriggerResult.Output.Trim() -ne "2") {
		throw "The supplier onboarding migration did not create the status and evidence guards."
	}

	$qualityResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('provider_quality_policies','provider_quality_states','supplier_provider_links','provider_quality_probe_schedules','provider_quality_observations','provider_price_verifications','provider_quality_rollups','provider_sla_events')" `
		-Operation "Validating Provider quality tables"
	if ($qualityResult.Output.Trim() -ne "8") {
		throw "The Provider quality migration did not create every required table."
	}
	$qualityTriggerResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN ('provider_quality_observations_immutable_trigger','provider_price_verifications_immutable_trigger','provider_quality_rollups_immutable_trigger','providers_seed_quality_state_trigger')" `
		-Operation "Validating Provider quality evidence and seed triggers"
	if ($qualityTriggerResult.Output.Trim() -ne "4") {
		throw "The Provider quality migration did not create every evidence guard."
	}

	$settlementResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('supplier_settlement_policy','supplier_payable_accrual','supplier_payable_entry','supplier_bill','supplier_bill_line','supplier_settlement_batch','supplier_settlement_item','supplier_usage_statement_match','supplier_appeal','supplier_payout_attempt','supplier_settlement_event')" `
		-Operation "Validating supplier settlement tables"
	if ($settlementResult.Output.Trim() -ne "11") {
		throw "The supplier settlement migration did not create every required table."
	}
	$settlementTriggerResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN ('supplier_payable_accrual_immutable_trigger','supplier_payable_entry_immutable_trigger','supplier_settlement_item_immutable_trigger','supplier_usage_statement_match_immutable_trigger','supplier_bill_line_immutable_trigger','supplier_settlement_event_immutable_trigger','supplier_bill_protect_trigger','supplier_settlement_item_scope_trigger','supplier_appeal_scope_trigger','supplier_statement_match_scope_trigger','supplier_seed_settlement_policy_trigger')" `
		-Operation "Validating supplier settlement evidence guards"
	if ($settlementTriggerResult.Output.Trim() -ne "11") {
		throw "The supplier settlement migration did not create every evidence guard."
	}

	$attestationResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='commercial_attestation_verification_audit'" `
		-Operation "Validating Migration 25 Attestation audit table"
	if ($attestationResult.Output.Trim() -ne "1") {
		throw "Migration 25 did not create the Attestation verification audit table."
	}
	$runtimeViewResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM information_schema.views WHERE table_schema='public' AND table_name IN ('commercial_runtime_provider_candidates_v2','commercial_runtime_supplier_candidates_v2')" `
		-Operation "Validating Migration 25 Runtime readiness views"
	if ($runtimeViewResult.Output.Trim() -ne "2") {
		throw "Migration 25 did not create both database-derived Runtime readiness views."
	}
	$decimalIntegrityResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT COALESCE(sum(invalid_rows),0) FROM commercial_decimal_integrity_v2()" `
		-Operation "Validating Migration 25 Decimal integrity scan"
	if ($decimalIntegrityResult.Output.Trim() -ne "0") {
		throw "Migration 25 Decimal integrity scan found invalid rows."
	}
	$attestationTriggerResult = Invoke-PsqlChecked -Database $script:testDatabase `
		-Sql "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname='commercial_attestation_verification_immutable'" `
		-Operation "Validating Migration 25 append-only Attestation audit"
	if ($attestationTriggerResult.Output.Trim() -ne "1") {
		throw "Migration 25 did not create the append-only Attestation audit trigger."
	}
}

function Get-LedgerSnapshot {
    $result = Invoke-PsqlChecked -Database $script:testDatabase `
        -Sql "SELECT version::text || '|' || name || '|' || checksum || '|' || applied_at::text FROM schema_migrations ORDER BY version" `
        -Operation "Snapshotting the migration ledger"
    return [string]::Join("`n", @(Get-NonEmptyLines -Text $result.Output))
}

function Wait-ForSuccessfulStartup {
    param([string]$Name)

    $deadline = [DateTime]::UtcNow.AddSeconds($script:StartupTimeoutSeconds)
    do {
        $state = Get-ContainerState -Name $Name
        if ($null -eq $state) { throw "The migration test container could not be inspected." }
        if ($state.Status -in @("exited", "dead")) {
            throw "The migration test container exited before successful startup; diagnostic output was suppressed."
        }

        $logs = Invoke-DockerRaw -Arguments @("container", "logs", $Name)
        if ($logs.ExitCode -eq 0 -and
            $logs.Output.Contains('"msg":"server_started"') -and
            $logs.Output.Contains('"component":"gateway"') -and
            $logs.Output.Contains('"component":"control_plane"')) {
            Assert-ExpectedLedger
            return
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)

    throw "Timed out waiting for the isolated RelayDock server to start; diagnostic output was suppressed."
}

function Assert-StartupRejected {
    param(
        [string]$Name,
        [string]$ExpectedMessage
    )

    Start-TestServer -Name $Name
    $deadline = [DateTime]::UtcNow.AddSeconds($script:StartupTimeoutSeconds)
    do {
        $state = Get-ContainerState -Name $Name
        if ($null -eq $state) { throw "The rejected migration test container could not be inspected." }
        if ($state.Status -in @("exited", "dead")) {
            if ($state.ExitCode -eq 0) {
                throw "RelayDock unexpectedly accepted an invalid migration ledger."
            }
            # Docker can publish the final stdout/stderr frame just after the
            # container state changes to exited. Poll briefly so a correct
            # rejection is not misclassified because of that logging race.
            $logDeadline = [DateTime]::UtcNow.AddSeconds(3)
            do {
                $logs = Invoke-DockerRaw -Arguments @("container", "logs", $Name)
                if ($logs.ExitCode -eq 0 -and $logs.Output.Contains($ExpectedMessage)) {
                    return
                }
                Start-Sleep -Milliseconds 100
            } while ([DateTime]::UtcNow -lt $logDeadline)
            throw "RelayDock rejected startup for an unexpected reason; diagnostic output was suppressed."
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)

    throw "RelayDock did not reject the invalid migration ledger before the timeout."
}

try {
    # Windows can return both docker.exe and an extensionless Docker shim for
    # one Get-Command lookup. Select one concrete executable; invoking the
    # unfiltered Source array would concatenate both paths and fail.
    $dockerCommand = @(Get-Command docker -CommandType Application -ErrorAction Stop) |
        Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_.Source) } |
        Select-Object -First 1
    if ($null -eq $dockerCommand) { throw "The Docker executable was not found." }
    $dockerExecutable = [string]$dockerCommand.Source

    if (-not [string]::IsNullOrWhiteSpace($env:DOCKER_HOST) -and -not (Test-LocalDockerEndpoint -Endpoint $env:DOCKER_HOST)) {
        throw "DOCKER_HOST must refer to a local socket or loopback endpoint for this test."
    }

    $contextResult = Invoke-DockerChecked -Arguments @("context", "inspect") -Operation "Inspecting the active Docker context"
    $contexts = @(ConvertFrom-DockerJson -Json $contextResult.Output -Operation "Docker context inspection")
    if ($contexts.Count -ne 1 -or -not (Test-LocalDockerEndpoint -Endpoint ([string]$contexts[0].Endpoints.docker.Host))) {
        throw "The active Docker context is not a local socket or loopback endpoint."
    }

    $networkResult = Invoke-DockerChecked -Arguments @("network", "inspect", $internalNetwork) -Operation "Inspecting the RelayDock internal network"
    $networks = @(ConvertFrom-DockerJson -Json $networkResult.Output -Operation "Docker network inspection")
    if ($networks.Count -ne 1 -or [string]$networks[0].Driver -ne "bridge" -or
        [string]$networks[0].Scope -ne "local" -or -not [bool]$networks[0].Internal) {
        throw "The required RelayDock network is not a local internal bridge."
    }

    $imageResult = Invoke-DockerChecked -Arguments @("image", "inspect", $serverImage) -Operation "Inspecting the final RelayDock image"
    $images = @(ConvertFrom-DockerJson -Json $imageResult.Output -Operation "Docker image inspection")
    if ($images.Count -ne 1) { throw "The required local RelayDock image was not found exactly once." }

    $postgresInspect = Get-ContainerInspect -Name $postgresContainer
    if (-not [bool]$postgresInspect.State.Running -or [string]$postgresInspect.Config.Image -notmatch '^postgres(:|@)') {
        throw "The expected local RelayDock PostgreSQL container is not running the PostgreSQL image."
    }
    if ([string]$postgresInspect.Config.Labels.'com.docker.compose.project' -ne "relaydock" -or
        [string]$postgresInspect.Config.Labels.'com.docker.compose.service' -ne "postgres") {
        throw "The PostgreSQL container is not the expected RelayDock Compose service."
    }
    $postgresNetworkProperty = $postgresInspect.NetworkSettings.Networks.PSObject.Properties[$internalNetwork]
    if ($null -eq $postgresNetworkProperty) {
        throw "The RelayDock PostgreSQL container is not attached to the required internal network."
    }

    $settings = Read-DotEnv -Path $EnvFile
    $requiredSettings = @(
        "POSTGRES_USER", "POSTGRES_DB", "DATABASE_URL", "REDIS_URL",
        "RELAYDOCK_MASTER_KEY", "RELAYDOCK_API_KEY_HMAC_SECRET",
        "RELAYDOCK_JWT_SECRET", "RELAYDOCK_ADMIN_PASSWORD"
    )
    foreach ($name in $requiredSettings) {
        if (-not $settings.ContainsKey($name) -or [string]::IsNullOrWhiteSpace([string]$settings[$name]) -or
            ([string]$settings[$name]).Contains("CHANGE_ME")) {
            throw "The Docker environment file is missing a non-placeholder $name value."
        }
    }
    $postgresUser = [string]$settings["POSTGRES_USER"]
    $postgresDatabase = [string]$settings["POSTGRES_DB"]

    $containerPostgresUser = @($postgresInspect.Config.Env | Where-Object { $_ -like "POSTGRES_USER=*" })
    $containerPostgresDatabase = @($postgresInspect.Config.Env | Where-Object { $_ -like "POSTGRES_DB=*" })
    if ($containerPostgresUser.Count -ne 1 -or $containerPostgresDatabase.Count -ne 1 -or
        $containerPostgresUser[0].Substring("POSTGRES_USER=".Length) -ne $postgresUser -or
        $containerPostgresDatabase[0].Substring("POSTGRES_DB=".Length) -ne $postgresDatabase) {
        throw "The Docker environment file does not match the running RelayDock PostgreSQL service."
    }

    try {
        $databaseUri = [Uri]([string]$settings["DATABASE_URL"])
    } catch {
        throw "DATABASE_URL is not a valid PostgreSQL URI."
    }
    if ($databaseUri.Scheme -notin @("postgres", "postgresql") -or $databaseUri.Port -ne 5432) {
        throw "DATABASE_URL must target PostgreSQL on the RelayDock internal container port."
    }

    $postgresAttachment = $postgresNetworkProperty.Value
    $allowedDatabaseHosts = @("postgres", $postgresContainer, [string]$postgresAttachment.IPAddress)
    $allowedDatabaseHosts += @($postgresAttachment.Aliases)
    $normalizedAllowedHosts = @($allowedDatabaseHosts | Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_) } | ForEach-Object { ([string]$_).ToLowerInvariant() })
    if ($normalizedAllowedHosts -notcontains $databaseUri.Host.ToLowerInvariant()) {
        throw "DATABASE_URL does not target the local RelayDock PostgreSQL container."
    }

    $databaseUser = $databaseUri.UserInfo.Split(":")[0]
    $databaseUser = [Uri]::UnescapeDataString($databaseUser)
    if ($databaseUser -ne $postgresUser) {
        throw "DATABASE_URL and POSTGRES_USER must identify the same local test role."
    }

    try {
        $databaseBuilder = New-Object System.UriBuilder($databaseUri)
        $databaseBuilder.Path = "/$testDatabase"
        $testDatabaseURL = $databaseBuilder.Uri.AbsoluteUri
    } catch {
        throw "DATABASE_URL could not be safely rewritten for the random test database."
    }
    $env:DATABASE_URL = $testDatabaseURL

    $createResult = Invoke-DockerRaw -Arguments @(
        "container", "exec", $postgresContainer,
        "createdb", "--no-password", "--username", $postgresUser,
        "--maintenance-db", $postgresDatabase,
        $testDatabase
    )
    if ($createResult.ExitCode -ne 0) {
        throw "Creating the random disposable migration database failed; diagnostic output was suppressed."
    }
    $databaseCreated = $true
    Write-Host "Created one random disposable database for the migration contract."

    $firstContainer = "relaydock-migration-$runID-first"
    Start-TestServer -Name $firstContainer
    Wait-ForSuccessfulStartup -Name $firstContainer
    $firstSnapshot = Get-LedgerSnapshot
    Remove-TestContainer -Name $firstContainer
    Write-Host "PASS empty database applied migrations 1:core through 25:commercial_attestation_and_decimal_hardening"

    $exactWrite = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "WITH target AS (UPDATE users SET monthly_cost_limit_exact=0.100000000001 WHERE id=(SELECT id FROM users ORDER BY id LIMIT 1) RETURNING monthly_cost_limit,monthly_cost_limit_exact) SELECT monthly_cost_limit::text||'|'||monthly_cost_limit_exact::text FROM target" `
        -Operation "Verifying exact-first compatibility write"
    if ($exactWrite.Output.Trim() -ne "0.10000000|0.100000000001") {
        throw "Exact-first compatibility write did not retain 12 decimal places and deterministic legacy rounding."
    }
    $legacyWrite = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "WITH target AS (UPDATE users SET monthly_cost_limit=0.2 WHERE id=(SELECT id FROM users ORDER BY id LIMIT 1) RETURNING monthly_cost_limit,monthly_cost_limit_exact) SELECT monthly_cost_limit::text||'|'||monthly_cost_limit_exact::text FROM target" `
        -Operation "Verifying legacy rollback compatibility write"
    if ($legacyWrite.Output.Trim() -ne "0.20000000|0.200000000000") {
        throw "Legacy-only compatibility write was not promoted exactly."
    }
    $maximumWrite = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "WITH target AS (UPDATE users SET monthly_cost_limit_exact=999999999999.999999990000 WHERE id=(SELECT id FROM users ORDER BY id LIMIT 1) RETURNING monthly_cost_limit,monthly_cost_limit_exact) SELECT monthly_cost_limit::text||'|'||monthly_cost_limit_exact::text FROM target" `
        -Operation "Verifying maximum compatible exact-money write"
    if ($maximumWrite.Output.Trim() -ne "999999999999.99999999|999999999999.999999990000") {
        throw "Maximum compatible exact amount was not retained without implicit rounding."
    }
    $negativeWrite = Invoke-PsqlRaw -Database $testDatabase `
        -Sql "UPDATE users SET monthly_cost_limit_exact=-0.000000000001 WHERE id=(SELECT id FROM users ORDER BY id LIMIT 1)"
    if ($negativeWrite.ExitCode -eq 0) { throw "Negative exact amount bypassed the database range constraint." }
    $overflowWrite = Invoke-PsqlRaw -Database $testDatabase `
        -Sql "UPDATE users SET monthly_cost_limit_exact=999999999999.999999994999 WHERE id=(SELECT id FROM users ORDER BY id LIMIT 1)"
    if ($overflowWrite.ExitCode -eq 0) { throw "Out-of-range exact amount bypassed the compatibility range constraint." }
    $nullWrite = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "WITH target AS (UPDATE users SET monthly_cost_limit=NULL,monthly_cost_limit_exact=NULL WHERE id=(SELECT id FROM users ORDER BY id LIMIT 1) RETURNING monthly_cost_limit,monthly_cost_limit_exact) SELECT COALESCE(monthly_cost_limit::text,'NULL')||'|'||COALESCE(monthly_cost_limit_exact::text,'NULL') FROM target" `
        -Operation "Verifying nullable exact-money write"
    if ($nullWrite.Output.Trim() -ne "NULL|NULL") { throw "Nullable exact amount was not preserved." }
    $zeroWrite = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "WITH target AS (UPDATE users SET monthly_cost_limit_exact=0 WHERE id=(SELECT id FROM users ORDER BY id LIMIT 1) RETURNING monthly_cost_limit,monthly_cost_limit_exact) SELECT monthly_cost_limit::text||'|'||monthly_cost_limit_exact::text FROM target" `
        -Operation "Verifying zero exact-money write"
    if ($zeroWrite.Output.Trim() -ne "0.00000000|0.000000000000") { throw "Zero exact amount was not preserved." }
    $moneyDifferences = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "SELECT COALESCE(sum(differences),0) FROM exact_money_migration_differences" `
        -Operation "Reconciling exact-money compatibility fields"
    if ($moneyDifferences.Output.Trim() -ne "0") { throw "Exact-money migration reconciliation reported differences." }
    Write-Host "PASS exact-money 12-place, maximum, negative, overflow, null, zero, legacy rollback, and zero-difference checks"

    $secondContainer = "relaydock-migration-$runID-second"
    Start-TestServer -Name $secondContainer
    Wait-ForSuccessfulStartup -Name $secondContainer
    $secondSnapshot = Get-LedgerSnapshot
    if ($secondSnapshot -ne $firstSnapshot) {
        throw "A second RelayDock startup changed the already-applied migration ledger."
    }
    Remove-TestContainer -Name $secondContainer
    Write-Host "PASS second startup was migration-idempotent"

    $insertUnknown = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "INSERT INTO schema_migrations(version,name,checksum) VALUES (999,'future_test',repeat('9',64)) RETURNING version" `
        -Operation "Injecting the unknown migration test row"
    if ($insertUnknown.Output.Trim() -ne "999") { throw "The unknown migration test row was not injected exactly once." }

    $unknownContainer = "relaydock-migration-$runID-unknown"
    Assert-StartupRejected -Name $unknownContainer -ExpectedMessage "database contains unknown schema migration version 999"
    Remove-TestContainer -Name $unknownContainer
    Write-Host "PASS unknown migration version 999 was rejected"

    $deleteUnknown = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "DELETE FROM schema_migrations WHERE version=999 RETURNING version" `
        -Operation "Removing the unknown migration test row"
    if ($deleteUnknown.Output.Trim() -ne "999") { throw "The unknown migration test row was not removed exactly once." }
    Assert-ExpectedLedger
    if ((Get-LedgerSnapshot) -ne $firstSnapshot) {
        throw "Removing the unknown migration row did not restore the original ledger."
    }

    $tamperChecksum = Invoke-PsqlChecked -Database $testDatabase `
        -Sql "UPDATE schema_migrations SET checksum=CASE WHEN checksum=repeat('0',64) THEN repeat('1',64) ELSE repeat('0',64) END WHERE version=4 RETURNING version" `
        -Operation "Tampering with migration 4 for the rejection test"
    if ($tamperChecksum.Output.Trim() -ne "4") { throw "Migration 4 was not tampered exactly once." }

    $checksumContainer = "relaydock-migration-$runID-checksum"
    Assert-StartupRejected -Name $checksumContainer -ExpectedMessage "migration 0004_project_route_soft_delete checksum mismatch"
    Remove-TestContainer -Name $checksumContainer
    Write-Host "PASS migration 4 checksum tampering was rejected"

    Recreate-TestDatabase
    Initialize-PopulatedV1Database
    $upgradeContainer = "relaydock-migration-$runID-upgrade"
    Start-TestServer -Name $upgradeContainer
    Wait-ForSuccessfulStartup -Name $upgradeContainer
    Assert-PopulatedV1Upgrade
    Remove-TestContainer -Name $upgradeContainer
    Write-Host "PASS populated V1 database upgraded through 25:commercial_attestation_and_decimal_hardening without losing legacy data"

    Recreate-TestDatabase
    Initialize-PopulatedV12FinancialDatabase
    $v12UpgradeContainer = "relaydock-migration-$runID-v12-upgrade"
    Start-TestServer -Name $v12UpgradeContainer
    Wait-ForSuccessfulStartup -Name $v12UpgradeContainer
    Assert-PopulatedV12FinancialUpgrade
    Remove-TestContainer -Name $v12UpgradeContainer
    Write-Host "PASS populated V12 funding and refund holds upgraded through 25 without becoming refundable"

    Write-Host "Migration contract verification passed for empty and populated-upgrade databases. Cleanup will now remove only this run's containers and random database."
} finally {
    foreach ($containerName in $createdContainers) {
        try {
            Remove-TestContainer -Name $containerName -BestEffort
        } catch {
            Write-Warning "A best-effort migration test container cleanup attempt failed."
        }
    }

    if ($databaseCreated) {
        if ($testDatabase -notmatch '^relaydock_migration_test_[0-9a-f]{20}$') {
            Write-Warning "Random database cleanup was refused because its generated name failed the safety check."
        } else {
            try {
                $dropResult = Invoke-DockerRaw -Arguments @(
                    "container", "exec", $postgresContainer,
                    "dropdb", "--no-password", "--if-exists", "--force",
                    "--username", $postgresUser,
                    "--maintenance-db", $postgresDatabase,
                    $testDatabase
                )
                if ($dropResult.ExitCode -ne 0) {
                    Write-Warning "Dropping the random migration test database failed; diagnostic output was suppressed."
                }
            } catch {
                Write-Warning "Dropping the random migration test database failed; diagnostic output was suppressed."
            }
        }
    }

    if ($originalDatabaseURLExists) {
        $env:DATABASE_URL = $originalDatabaseURL
    } else {
        Remove-Item Env:DATABASE_URL -ErrorAction SilentlyContinue
    }
    $testDatabaseURL = $null
}
