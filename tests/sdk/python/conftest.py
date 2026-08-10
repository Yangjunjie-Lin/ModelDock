from __future__ import annotations

import os
from collections.abc import Iterator

import httpx
import pytest
from openai import OpenAI


def _required(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value or "replace" in value.lower():
        pytest.skip(f"{name} is not configured for the SDK integration suite")
    return value


@pytest.fixture(scope="session")
def relaydock_base_url() -> str:
    return os.getenv("RELAYDOCK_BASE_URL", "http://127.0.0.1:8080/v1").rstrip("/")


@pytest.fixture(scope="session")
def chat_model() -> str:
    return os.getenv("RELAYDOCK_CHAT_MODEL", "gpt-default")


@pytest.fixture(scope="session")
def embedding_model() -> str:
    return os.getenv("RELAYDOCK_EMBEDDING_MODEL", "embedding-default")


@pytest.fixture(scope="session")
def client(relaydock_base_url: str) -> Iterator[OpenAI]:
    # The suite always targets a local RelayDock listener. Ignoring host-wide
    # proxy settings prevents corporate/system proxies from intercepting
    # loopback requests on Windows while still exercising the official SDK.
    http_client = httpx.Client(trust_env=False)
    sdk = OpenAI(
        api_key=_required("RELAYDOCK_API_KEY"),
        base_url=relaydock_base_url,
        timeout=20.0,
        max_retries=0,
        http_client=http_client,
    )
    try:
        yield sdk
    finally:
        sdk.close()
