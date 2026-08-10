# Official Python SDK compatibility suite

This suite exercises RelayDock through the official `openai` Python package. It
covers:

- `client.models.list()`;
- `client.responses.create()`;
- Responses SSE streaming;
- `client.chat.completions.create()`;
- Chat Completions SSE streaming;
- `client.embeddings.create()`.

It performs requests only when `RELAYDOCK_API_KEY` is set to a non-placeholder
value. This keeps ordinary unit-test runs offline and prevents accidental use of
a real provider credential. The test HTTP client ignores machine-wide proxy
settings for loopback requests so a corporate Windows proxy cannot intercept
the local gateway.

```powershell
py -m venv .venv
.\.venv\Scripts\python -m pip install -r tests\sdk\python\requirements.txt
$env:RELAYDOCK_BASE_URL = "http://127.0.0.1:8080/v1"
$env:RELAYDOCK_API_KEY = "rdk_test_..."
$env:RELAYDOCK_CHAT_MODEL = "gpt-default"
$env:RELAYDOCK_EMBEDDING_MODEL = "embedding-default"
.\.venv\Scripts\python -m pytest -q tests\sdk\python
```

Use the optional Compose `mock-openai` profile and a dedicated test database to
avoid billable upstream calls. Never place an OpenAI provider key in these test
variables; the SDK receives only the downstream RelayDock key.
