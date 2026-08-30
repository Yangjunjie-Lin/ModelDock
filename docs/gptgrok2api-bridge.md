# gptGrok2api multi-provider bridge

RelayDock can use an authorized gptGrok2api deployment as a custom
OpenAI-compatible Provider. The integration uses only the deployment's public
API contract (`/v1/models`, `/v1/responses`, and `/v1/chat/completions`) and an
already-issued bearer key. It does not import consumer account passwords,
browser cookies, registration sessions, temporary-mail workflows, CAPTCHA
handling, proxy rotation, or third-party account creation code.

## Configure the Provider

1. Add the endpoint host to `RELAYDOCK_PROVIDER_ALLOWED_HOSTS`. The included
   example permits `pro.muyuai.top`; remove it when that bridge is not in use.
2. In **Admin > Providers**, create a Provider with:
   - Provider type: `OpenAI-compatible / custom gateway`
   - Base URL: `https://pro.muyuai.top/v1`
   - Commercial/resale/region fields set to the result of your own review
3. In **Admin > Credentials**, select that Provider and add an already-issued
   API key or bearer token. **Validate & save** discovers models before the
   credential becomes active.
4. Create a Provider-owned credential group, sync models, and configure routes.

The Provider remains fail-closed for production traffic until its commercial
status, resale permission, regions, pricing, and kill switch satisfy RelayDock's
normal admission policy. Technical connectivity is not commercial approval.

## Probe the domain

The default probe verifies HTTPS reachability and the OpenAI-compatible `401`
error envelope without requiring a secret:

```powershell
pwsh ./scripts/verify-gptgrok2api-bridge.ps1
```

For authenticated model discovery, inject the bridge key without placing it on
the command line or in source control:

```powershell
$env:GPTGROK2API_API_KEY = '<authorized bridge key>'
pwsh ./scripts/verify-gptgrok2api-bridge.ps1 -RequireAuthenticated
```

The optional `-RunChatCompletion` switch sends one minimal two-token completion
and may consume upstream quota. It is never enabled by default.
For an isolated local source checkout, `-AllowHttp` can be used explicitly;
RelayDock production Provider endpoints should remain HTTPS-only.

`gpt.muyuai.top` is intentionally not in the sample allowlist: during the
2026-08-30 verification it did not present a usable HTTPS endpoint, while
`https://pro.muyuai.top/v1/models` returned the expected OpenAI-compatible
authentication response.
