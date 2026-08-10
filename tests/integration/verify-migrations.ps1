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
    "4:project_route_soft_delete"
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
    Write-Host "PASS empty database applied migrations 1:core through 4:project_route_soft_delete"

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

    Write-Host "Migration contract verification passed. Cleanup will now remove only this run's containers and random database."
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
