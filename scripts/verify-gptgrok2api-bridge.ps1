param(
    [string]$BaseUrl = "https://pro.muyuai.top/v1",
    [string]$ApiKey = "",
    [string]$ApiKeyEnv = "GPTGROK2API_API_KEY",
    [switch]$RequireAuthenticated,
    [switch]$RunChatCompletion,
    [switch]$AllowHttp
)

$ErrorActionPreference = "Stop"

function Invoke-BridgeRequest {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$Token,
        [string]$Method = "GET",
        [string]$Body = ""
    )
    $parameters = @{
        Uri = $Uri
        Method = $Method
        Headers = @{ Authorization = "Bearer $Token" }
        TimeoutSec = 30
        SkipHttpErrorCheck = $true
    }
    if ($Body) {
        $parameters.ContentType = "application/json"
        $parameters.Body = $Body
    }
    Invoke-WebRequest @parameters
}

$base = $BaseUrl.TrimEnd("/")
$parsed = $null
if (-not [Uri]::TryCreate($base, [UriKind]::Absolute, [ref]$parsed) -or
    ($parsed.Scheme -ne "https" -and -not ($AllowHttp -and $parsed.Scheme -eq "http"))) {
    throw "BaseUrl must be an absolute HTTPS URL (or use -AllowHttp for an isolated local probe)."
}

if (-not $ApiKey) {
    $ApiKey = [Environment]::GetEnvironmentVariable($ApiKeyEnv)
}
$authenticated = -not [string]::IsNullOrWhiteSpace($ApiKey)
if ($RequireAuthenticated -and -not $authenticated) {
    throw "Authenticated probe requested, but neither -ApiKey nor $ApiKeyEnv is set."
}
if (-not $authenticated) {
    $ApiKey = "relaydock-invalid-connectivity-probe"
}

$modelsResponse = Invoke-BridgeRequest -Uri "$base/models" -Token $ApiKey
$contentType = [string]$modelsResponse.Headers."Content-Type"
$payload = $null
try {
    $payload = $modelsResponse.Content | ConvertFrom-Json
} catch {
    throw "gptGrok2api did not return JSON from $base/models (HTTP $([int]$modelsResponse.StatusCode))."
}

if (-not $authenticated) {
    if ([int]$modelsResponse.StatusCode -ne 401) {
        throw "Anonymous boundary probe expected HTTP 401, received $([int]$modelsResponse.StatusCode)."
    }
    $errorType = [string]$payload.error.type
    if (-not $errorType) {
        throw "Authentication failure did not use the OpenAI-compatible error envelope."
    }
    [pscustomobject]@{
        ok = $true
        mode = "authentication_boundary"
        base_url = $base
        status = [int]$modelsResponse.StatusCode
        content_type = $contentType
        error_type = $errorType
        note = "Connectivity and OpenAI-compatible authentication semantics verified; set $ApiKeyEnv for model discovery."
    } | ConvertTo-Json -Depth 5
    exit 0
}

if ([int]$modelsResponse.StatusCode -ne 200) {
    throw "Authenticated model discovery failed with HTTP $([int]$modelsResponse.StatusCode)."
}
$models = @($payload.data | ForEach-Object { [string]$_.id } | Where-Object { $_ })
if ($models.Count -eq 0) {
    throw "Authenticated model discovery returned no models."
}

$chatStatus = $null
$chatModel = $null
if ($RunChatCompletion) {
    $chatModel = $models | Where-Object { $_ -notmatch "(?i)(image|video|embedding|tts|audio)" } | Select-Object -First 1
    if (-not $chatModel) {
        throw "No chat-capable model was found for the optional completion probe."
    }
    $chatBody = @{
        model = $chatModel
        messages = @(@{ role = "user"; content = "Reply with OK." })
        max_tokens = 2
        stream = $false
    } | ConvertTo-Json -Depth 6 -Compress
    $chatResponse = Invoke-BridgeRequest -Uri "$base/chat/completions" -Token $ApiKey -Method "POST" -Body $chatBody
    $chatStatus = [int]$chatResponse.StatusCode
    if ($chatStatus -ne 200) {
        throw "Minimal chat completion probe failed with HTTP $chatStatus."
    }
}

[pscustomobject]@{
    ok = $true
    mode = if ($RunChatCompletion) { "models_and_chat" } else { "models" }
    base_url = $base
    status = [int]$modelsResponse.StatusCode
    model_count = $models.Count
    model_sample = @($models | Select-Object -First 12)
    chat_status = $chatStatus
    chat_model = $chatModel
} | ConvertTo-Json -Depth 5
