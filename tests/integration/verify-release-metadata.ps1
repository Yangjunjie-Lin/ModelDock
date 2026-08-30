[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Commit = "",
    [string]$BuildTime = "",
    [string]$Image = "",
    [string]$Platform = "linux/amd64",
    [switch]$VerifyReproducible
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
if (-not $Version) {
    $source = [System.IO.File]::ReadAllText((Join-Path $repoRoot "internal\version\version.go"))
    $match = [regex]::Match($source, '(?m)^\s*Current\s*=\s*"([^"]+)"\s*$')
    if (-not $match.Success) { throw "Could not read internal/version.Current." }
    $Version = $match.Groups[1].Value
}
if (-not $Image) {
    $Image = "modeldock/release-metadata:$([Guid]::NewGuid().ToString('N'))"
}
if ($Image -notmatch '^[a-z0-9][a-z0-9._/-]*:[a-zA-Z0-9._-]+$') {
    throw "The test image name is not safe."
}
if ($Platform -notin @("linux/amd64")) {
    throw "Release metadata verification currently supports the published linux/amd64 platform only."
}
if (-not $Commit) {
    $Commit = (& git -C $repoRoot rev-parse HEAD).Trim()
}
if ($Commit -notmatch '^[0-9a-f]{40}$') {
    throw "Commit must be a full lowercase 40-character Git SHA."
}
if (-not $BuildTime) {
    $BuildTime = (& git -C $repoRoot show -s --format=%cI $Commit).Trim()
}
try {
    $parsedBuildTime = [DateTimeOffset]::Parse($BuildTime).ToUniversalTime()
} catch {
    throw "BuildTime must be an ISO-8601 timestamp."
}
$BuildTime = $parsedBuildTime.ToString("yyyy-MM-ddTHH:mm:ssZ")
$sourceDateEpoch = (& git -C $repoRoot show -s --format=%ct $Commit).Trim()
if ($sourceDateEpoch -notmatch '^[0-9]+$') {
    throw "Git did not return a valid source timestamp."
}

& (Join-Path $repoRoot "scripts\verify-release.ps1") -Version $Version -Commit $Commit

function Build-TestImage {
    param([string]$Tag, [switch]$NoCache)
    $arguments = @(
        "build", "--provenance=false", "--platform", $Platform, "--file", "deploy/docker/Dockerfile.relaydock", "--tag", $Tag,
        "--build-arg", "VERSION=$Version", "--build-arg", "COMMIT=$Commit",
        "--build-arg", "BUILD_DATE=$BuildTime", "--build-arg", "SOURCE_DATE_EPOCH=$sourceDateEpoch"
    )
    if ($NoCache) { $arguments += "--no-cache" }
    $arguments += "."
    & docker @arguments
    if ($LASTEXITCODE -ne 0) { throw "Building release metadata test image failed." }
}

function Build-ReproducibleArchive {
    param([string]$Tag, [string]$Destination, [switch]$NoCache)
    $arguments = @(
        "buildx", "build", "--provenance=false", "--platform", $Platform,
        "--file", "deploy/docker/Dockerfile.relaydock", "--tag", $Tag,
        "--build-arg", "VERSION=$Version", "--build-arg", "COMMIT=$Commit",
        "--build-arg", "BUILD_DATE=$BuildTime", "--build-arg", "SOURCE_DATE_EPOCH=$sourceDateEpoch",
        "--output", "type=oci,dest=$Destination,rewrite-timestamp=true"
    )
    if ($NoCache) { $arguments += "--no-cache" }
    $arguments += "."
    & docker @arguments
    if ($LASTEXITCODE -ne 0) { throw "Building reproducible OCI archive failed." }
}

function Get-OCIManifestDigest {
    param([string]$Archive)
    $indexLines = @(& tar -xOf $Archive index.json)
    if ($LASTEXITCODE -ne 0) { throw "Reading the OCI archive index failed." }
    $index = ([string]::Join([Environment]::NewLine, $indexLines)) | ConvertFrom-Json
    $manifests = @($index.manifests)
    if ($manifests.Count -ne 1 -or [string]$manifests[0].digest -notmatch '^sha256:[0-9a-f]{64}$') {
        throw "The OCI archive did not contain exactly one valid image manifest."
    }
    return [string]$manifests[0].digest
}

function Read-Label {
    param([string]$Tag, [string]$Name)
    $raw = @(& docker image inspect --format "{{ index .Config.Labels `"$Name`" }}" $Tag)
    if ($LASTEXITCODE -ne 0) { throw "Inspecting release metadata test image failed." }
    if ($raw.Count -ne 1) { throw "Image inspection returned an unexpected result count." }
    return [string]$raw[0]
}

$rebuildImage = "$Image-rebuild"
$reproRoot = [System.IO.Path]::GetFullPath((Join-Path $repoRoot ".cache\release-repro-$([Guid]::NewGuid().ToString('N'))"))
if (-not $reproRoot.StartsWith((Join-Path $repoRoot ".cache") + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "The generated reproducibility directory escaped the repository cache."
}
try {
    foreach ($tag in @($Image, $rebuildImage)) {
        $savedPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "SilentlyContinue"
            & docker image inspect $tag *> $null
            $tagExists = $LASTEXITCODE -eq 0
        } finally {
            $ErrorActionPreference = $savedPreference
        }
        if ($tagExists) { throw "Release metadata test refused to overwrite existing image $tag." }
    }
    Build-TestImage -Tag $Image
    $expectedLabels = @{
        "org.opencontainers.image.title" = "ModelDock Server"
        "org.opencontainers.image.version" = $Version
        "org.opencontainers.image.revision" = $Commit
        "org.opencontainers.image.created" = $BuildTime
        "org.opencontainers.image.source" = "https://github.com/Yangjunjie-Lin/ModelDock"
    }
    foreach ($entry in $expectedLabels.GetEnumerator()) {
        $actual = Read-Label -Tag $Image -Name $entry.Key
        if ($actual -ne $entry.Value) {
            throw "OCI label $($entry.Key) was '$actual', expected '$($entry.Value)'."
        }
    }

    $versionOutput = (& docker run --rm $Image --version).Trim()
    if ($LASTEXITCODE -ne 0) { throw "Running the image version command failed." }
    $expectedOutput = "ModelDock $Version (RelayDock compatibility; commit $Commit; built $BuildTime)"
    if ($versionOutput -ne $expectedOutput) {
        throw "Version output did not match the injected release identity."
    }

    if ($VerifyReproducible) {
        [System.IO.Directory]::CreateDirectory($reproRoot) | Out-Null
        $firstArchive = Join-Path $reproRoot "first.oci.tar"
        $secondArchive = Join-Path $reproRoot "second.oci.tar"
        Build-ReproducibleArchive -Tag $Image -Destination $firstArchive
        Build-ReproducibleArchive -Tag $rebuildImage -Destination $secondArchive -NoCache
        $firstDigest = Get-OCIManifestDigest -Archive $firstArchive
        $secondDigest = Get-OCIManifestDigest -Archive $secondArchive
        if ($firstDigest -ne $secondDigest) {
            throw "Two clean reproducible OCI exports from identical commit inputs produced different digests."
        }
        Write-Host "PASS reproducible OCI manifest $firstDigest"
    }
    Write-Host "PASS release image metadata and compatibility identity"
} finally {
    $savedPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "SilentlyContinue"
        foreach ($tag in @($Image, $rebuildImage)) {
            & docker image inspect $tag *> $null
            if ($LASTEXITCODE -eq 0) { & docker image rm --force $tag *> $null }
        }
        if ([System.IO.Directory]::Exists($reproRoot)) {
            Remove-Item -LiteralPath $reproRoot -Recurse -Force
        }
    } finally {
        $ErrorActionPreference = $savedPreference
    }
}

# A missing cleanup-only image is expected; make the successful script result
# explicit so its native Docker cleanup status cannot leak into CI.
$global:LASTEXITCODE = 0
