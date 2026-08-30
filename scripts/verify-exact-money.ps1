[CmdletBinding()]
param(
    [string]$RepositoryRoot = "",
    [string]$AllowlistPath = ""
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

$repoRoot = if ($RepositoryRoot) {
    [System.IO.Path]::GetFullPath($RepositoryRoot)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}
if (-not $AllowlistPath) {
    $AllowlistPath = Join-Path $repoRoot "scripts\exact-money-allowlist.json"
}
$allowlist = Get-Content -LiteralPath $AllowlistPath -Raw | ConvertFrom-Json
if ($allowlist.schema_version -ne 1) {
    throw "Unsupported exact-money allowlist schema version."
}

$floatPattern = 'float32|float64|::float8|DOUBLE\s+PRECISION|\bREAL\b|ParseFloat|FormatFloat'
$moneyTypePattern = '(?i)(amount|cost|price|balance|budget|fee|margin|savings|refund|payable|invoice)[A-Za-z0-9_]*\??\s*[: ]\s*(float32|float64|number)'
$moneyNumberPattern = '(?i)(\bNumber|\bparseFloat)\([^\)]*(amount|cost|price|balance|budget|fee|margin|savings|refund|payable|invoice)'
$moneySQLPattern = '(?i)((amount|cost|price|balance|budget|fee|margin|savings|refund|payable|invoice)[A-Za-z0-9_\.]*.*::float8|sum\([^\)]*(amount|cost|price|balance|fee|margin|savings|refund|payable|invoice)[^\)]*\)::float8)'
$legacyMoneyReadPattern = '(?i)(SELECT|sum\(|COALESCE\()[^\r\n`]*(monthly_cost_limit|input_price|cached_input_price|output_price|estimated_cost|reference_cost|savings_amount)\b'
$exactCompanionPattern = '(?i)(monthly_cost_limit_exact|input_price_exact|cached_input_price_exact|output_price_exact|estimated_cost_exact|reference_cost_exact|savings_amount_exact)'
$dynamicMustDecimalPattern = 'domain\.MustDecimal\((?!\s*")'
$unsafeCommercialDecimalCastPattern = 'domain\.Decimal\('
$commercialDecimalCastPaths = @(
    "internal/store/funding.go", "internal/store/payments.go", "internal/store/pricing.go",
    "internal/store/resources.go", "internal/store/subscriptions.go", "internal/store/supplier_settlement.go",
    "internal/store/v2_control.go", "internal/store/v2_usage.go", "internal/store/v2_tenants.go",
    "internal/store/marketplace_enterprise.go", "internal/server/finance.go"
)
$extensions = @(".go", ".sql", ".ts", ".tsx")
$roots = @("internal", "migrations", "apps", "tests")
$findings = [System.Collections.Generic.List[object]]::new()

foreach ($root in $roots) {
    $absoluteRoot = Join-Path $repoRoot $root
    if (-not (Test-Path -LiteralPath $absoluteRoot -PathType Container)) { continue }
    foreach ($file in Get-ChildItem -LiteralPath $absoluteRoot -Recurse -File) {
        if ($file.Extension -notin $extensions -or $file.FullName -match '[\\/](node_modules|dist|coverage)[\\/]') { continue }
        $relative = $file.FullName.Substring($repoRoot.Length).TrimStart('\', '/').Replace('\', '/')
        $lines = [System.IO.File]::ReadAllLines($file.FullName)
        for ($index = 0; $index -lt $lines.Length; $index++) {
            $line = $lines[$index]
            $trimmed = $line.Trim()
            if ($trimmed.StartsWith("//") -or $trimmed.StartsWith("/*") -or $trimmed.StartsWith("*")) { continue }
            $legacyMoneyRead = $file.Extension -in @(".go", ".sql") -and $line -match $legacyMoneyReadPattern -and $line -notmatch $exactCompanionPattern
            $unsafeCast = $relative -in $commercialDecimalCastPaths -and $line -match $unsafeCommercialDecimalCastPattern
            $candidate = $line -match $floatPattern -or $line -match $moneyTypePattern -or $line -match $moneyNumberPattern -or
                $line -match $moneySQLPattern -or $legacyMoneyRead -or $line -match $dynamicMustDecimalPattern -or $unsafeCast
            if (-not $candidate) { continue }
            $allowed = $false
            foreach ($entry in $allowlist.entries) {
                if ($relative -eq [string]$entry.path -and $line -match [string]$entry.line_pattern) {
                    if ([string]::IsNullOrWhiteSpace([string]$entry.reason)) {
                        throw "Allowlist entry for $relative is missing a reason."
                    }
                    $allowed = $true
                    break
                }
            }
            if (-not $allowed) {
                $findings.Add([pscustomobject]@{ Path = $relative; Line = $index + 1; Text = $trimmed })
            }
        }
    }
}

if ($findings.Count -gt 0) {
    $findings | Sort-Object Path, Line | Format-Table -AutoSize | Out-String | Write-Host
    throw "Exact-money verification found $($findings.Count) unapproved floating-point or JavaScript Number path(s)."
}

$decimalSource = Get-Content -LiteralPath (Join-Path $repoRoot "internal/domain/domain.go") -Raw
$requiredContracts = @(
    'func ParseDecimal\(value string\) \(Decimal, error\)',
    'func MustDecimal\(value string\) Decimal',
    'func \(d Decimal\) Add\(other Decimal\) \(Decimal, error\)',
    'func \(d Decimal\) Subtract\(other Decimal\) \(Decimal, error\)',
    'func \(d Decimal\) Multiply\(other Decimal\) \(Decimal, error\)',
    'func \(d Decimal\) Compare\(other Decimal\) \(int, error\)'
)
foreach ($contract in $requiredContracts) {
    if ($decimalSource -notmatch $contract) { throw "Decimal error-return contract is missing: $contract" }
}
if ($decimalSource -match 'decimalRatOrZero|invalid[^\r\n]*treated as zero') {
    throw "A silent-zero Decimal fallback remains in the commercial amount type."
}

Write-Host "PASS exact-money static verification; Decimal arithmetic returns errors and retained floats are non-monetary."
