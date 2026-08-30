[CmdletBinding()]
param()

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
if (Test-Path variable:PSNativeCommandUseErrorActionPreference) {
    $PSNativeCommandUseErrorActionPreference = $false
}

$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$validator = Join-Path $repoRoot "scripts\verify-release-tag.ps1"
$cacheRoot = [System.IO.Path]::GetFullPath((Join-Path $repoRoot ".cache\release-tag-contract-tests"))
$testRoot = [System.IO.Path]::GetFullPath((Join-Path $cacheRoot ([Guid]::NewGuid().ToString("N"))))
if (-not $testRoot.StartsWith($cacheRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Generated release Tag test directory escaped the repository cache."
}

function Invoke-Git {
    param(
        [Parameter(Mandatory = $true)]
        [string]$WorkingDirectory,
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )
    & git -C $WorkingDirectory @Arguments *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Git command failed in release Tag contract test: git $($Arguments -join ' ')"
    }
}

function Assert-Contract {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Runner,
        [Parameter(Mandatory = $true)]
        [string]$Ref,
        [Parameter(Mandatory = $true)]
        [string]$RefType,
        [Parameter(Mandatory = $true)]
        [string]$Commit,
        [Parameter(Mandatory = $true)]
        [bool]$ShouldPass
    )
    & (Join-Path $PSHOME "pwsh") -NoProfile -File $validator -RepositoryRoot $Runner -Ref $Ref -RefType $RefType -Commit $Commit *> $null
    $passed = $LASTEXITCODE -eq 0
    if ($passed -ne $ShouldPass) {
        throw "Release Tag contract result for $Ref was $passed; expected $ShouldPass."
    }
}

try {
    [System.IO.Directory]::CreateDirectory($testRoot) | Out-Null
    $remote = Join-Path $testRoot "remote.git"
    $source = Join-Path $testRoot "source"
    $runner = Join-Path $testRoot "runner"
    & git init --bare --quiet $remote
    if ($LASTEXITCODE -ne 0) { throw "Could not initialize release Tag test remote." }
    Invoke-Git $remote @("symbolic-ref", "HEAD", "refs/heads/main")
    & git init --quiet $source
    if ($LASTEXITCODE -ne 0) { throw "Could not initialize release Tag test source." }
    Invoke-Git $source @("config", "user.name", "ModelDock Release Test")
    Invoke-Git $source @("config", "user.email", "release-test@invalid.example")
    Invoke-Git $source @("remote", "add", "origin", $remote)

    [System.IO.File]::WriteAllText((Join-Path $source "payload.txt"), "first`n", (New-Object System.Text.UTF8Encoding($false)))
    Invoke-Git $source @("add", "payload.txt")
    Invoke-Git $source @("commit", "--quiet", "-m", "first")
    $firstCommit = [string]::Join("", @(& git -C $source rev-parse HEAD)).Trim()
    Invoke-Git $source @("tag", "-a", "v1.2.3-preview.1", "-m", "annotated preview")
    Invoke-Git $source @("tag", "v1.2.3-lightweight")

    [System.IO.File]::WriteAllText((Join-Path $source "payload.txt"), "second`n", (New-Object System.Text.UTF8Encoding($false)))
    Invoke-Git $source @("add", "payload.txt")
    Invoke-Git $source @("commit", "--quiet", "-m", "second")
    $secondCommit = [string]::Join("", @(& git -C $source rev-parse HEAD)).Trim()
    Invoke-Git $source @("tag", "-a", "v1.2.3-wrong-commit", "-m", "wrong commit")
    Invoke-Git $source @("push", "--quiet", "origin", "HEAD:refs/heads/main", "refs/tags/v1.2.3-preview.1", "refs/tags/v1.2.3-lightweight", "refs/tags/v1.2.3-wrong-commit")

    & git clone --quiet --no-tags $remote $runner
    if ($LASTEXITCODE -ne 0) { throw "Could not clone release Tag test runner." }
    Invoke-Git $runner @("checkout", "--quiet", "--detach", $firstCommit)

    # Reproduce actions/checkout's peeled local Tag, then prove the validator
    # restores the annotated object from origin before checking its type.
    Invoke-Git $runner @("tag", "v1.2.3-preview.1", $firstCommit)
    Assert-Contract $runner "refs/tags/v1.2.3-preview.1" "tag" $firstCommit $true
    $restoredType = [string]::Join("", @(& git -C $runner cat-file -t refs/tags/v1.2.3-preview.1)).Trim()
    if ($restoredType -cne "tag") { throw "The remote annotated Tag object was not restored." }

    Assert-Contract $runner "refs/tags/v1.2.3-lightweight" "tag" $firstCommit $false
    Assert-Contract $runner "refs/tags/v1.2.3-wrong-commit" "tag" $firstCommit $false
    Assert-Contract $runner "refs/tags/v1.2.3-preview.1" "branch" $firstCommit $false
    if ($secondCommit -ceq $firstCommit) { throw "Release Tag test commits unexpectedly match." }

    Write-Host "PASS release Tag restoration and negative identity scenarios"
} finally {
    if ([System.IO.Directory]::Exists($testRoot)) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}

$global:LASTEXITCODE = 0
