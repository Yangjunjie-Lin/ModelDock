"""Small deterministic OpenAI-compatible upstream used only for offline tests.

This service intentionally implements the public inference shapes RelayDock
proxies. It does not emulate accounts, browser sessions, billing, or provider
administration.
"""

from __future__ import annotations

import hashlib
import hmac
import json
import os
import threading
import time
import uuid
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


MAX_BODY_BYTES = 2 * 1024 * 1024
WEBHOOK_DELIVERIES: list[dict[str, Any]] = []
WEBHOOK_LOCK = threading.Lock()
UPSTREAM_REQUESTS: list[dict[str, Any]] = []
TEST_SCENARIO: dict[str, Any] = {}
TEST_LOCK = threading.Lock()


def now() -> int:
    return int(time.time())


def object_id(prefix: str) -> str:
    return f"{prefix}_{uuid.uuid4().hex[:24]}"


def usage(input_tokens: int = 4, output_tokens: int = 5) -> dict[str, Any]:
    return {
        "input_tokens": input_tokens,
        "input_tokens_details": {"cached_tokens": 0},
        "output_tokens": output_tokens,
        "output_tokens_details": {"reasoning_tokens": 0},
        "total_tokens": input_tokens + output_tokens,
    }


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "RelayDockMockOpenAI/1.0"

    def log_message(self, fmt: str, *args: Any) -> None:
        # Never log request bodies or Authorization headers.
        print(json.dumps({"service": "mock-openai", "message": fmt % args}))

    def _request_id(self) -> str:
        return f"req_{uuid.uuid4().hex}"

    def _headers(
        self,
        status: int,
        content_type: str,
        length: int | None = None,
        extra: dict[str, str] | None = None,
    ) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Request-Id", self._request_id())
        self.send_header("X-RateLimit-Limit-Requests", "1000")
        self.send_header("X-RateLimit-Remaining-Requests", "999")
        if length is not None:
            self.send_header("Content-Length", str(length))
        for key, value in (extra or {}).items():
            self.send_header(key, value)
        self.end_headers()

    def _json(self, status: int, payload: dict[str, Any]) -> None:
        data = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        self._headers(status, "application/json", len(data))
        self.wfile.write(data)

    def _json_headers(
        self, status: int, payload: dict[str, Any], headers: dict[str, str]
    ) -> None:
        data = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        self._headers(status, "application/json", len(data), headers)
        self.wfile.write(data)

    def _empty(self, status: int) -> None:
        self._headers(status, "application/json", 0)

    def _error(self, status: int, code: str, message: str) -> None:
        self._json(
            status,
            {
                "error": {
                    "message": message,
                    "type": "invalid_request_error" if status < 500 else "server_error",
                    "param": None,
                    "code": code,
                }
            },
        )

    def _authorized(self) -> bool:
        expected = os.getenv("MOCK_OPENAI_API_KEY", "mock-upstream-key")
        return self.headers.get("Authorization", "") == f"Bearer {expected}"

    def _test_authorized(self) -> bool:
        expected = os.getenv("MOCK_TEST_TOKEN", "relaydock-test-control")
        return self.headers.get("X-RelayDock-Test-Token", "") == expected

    def _body(self) -> dict[str, Any] | None:
        raw_length = self.headers.get("Content-Length", "0")
        try:
            length = int(raw_length)
        except ValueError:
            self._error(
                HTTPStatus.BAD_REQUEST,
                "invalid_content_length",
                "Invalid Content-Length",
            )
            return None
        if length < 0 or length > MAX_BODY_BYTES:
            self._error(
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
                "request_too_large",
                "Request body is too large",
            )
            return None
        try:
            payload = json.loads(self.rfile.read(length) or b"{}")
        except (UnicodeDecodeError, json.JSONDecodeError):
            self._error(
                HTTPStatus.BAD_REQUEST,
                "invalid_json",
                "Request body must be valid JSON",
            )
            return None
        if not isinstance(payload, dict):
            self._error(
                HTTPStatus.BAD_REQUEST,
                "invalid_json",
                "Request body must be a JSON object",
            )
            return None
        return payload

    def _raw_body(self) -> bytes | None:
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            self._error(
                HTTPStatus.BAD_REQUEST,
                "invalid_content_length",
                "Invalid Content-Length",
            )
            return None
        if length < 0 or length > MAX_BODY_BYTES:
            self._error(
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
                "request_too_large",
                "Request body is too large",
            )
            return None
        return self.rfile.read(length)

    def do_GET(self) -> None:  # noqa: N802
        path = self.path.split("?", 1)[0]
        if path == "/healthz":
            self._json(HTTPStatus.OK, {"status": "ok"})
            return
        if path == "/webhooks/received":
            with WEBHOOK_LOCK:
                deliveries = list(WEBHOOK_DELIVERIES)
            self._json(HTTPStatus.OK, {"data": deliveries})
            return
        if path == "/__test/requests":
            if not self._test_authorized():
                self._error(
                    HTTPStatus.UNAUTHORIZED,
                    "invalid_test_token",
                    "Invalid test token",
                )
                return
            with TEST_LOCK:
                requests = list(UPSTREAM_REQUESTS)
                scenario = dict(TEST_SCENARIO)
            self._json(HTTPStatus.OK, {"data": requests, "scenario": scenario})
            return
        if path == "/v1/models":
            if not self._authorized():
                self._error(
                    HTTPStatus.UNAUTHORIZED, "invalid_api_key", "Invalid API key"
                )
                return
            created = now()
            self._json(
                HTTPStatus.OK,
                {
                    "object": "list",
                    "data": [
                        {
                            "id": "mock-chat",
                            "object": "model",
                            "created": created,
                            "owned_by": "relaydock-mock",
                        },
                        {
                            "id": "mock-embedding",
                            "object": "model",
                            "created": created,
                            "owned_by": "relaydock-mock",
                        },
                    ],
                },
            )
            return
        self._error(HTTPStatus.NOT_FOUND, "not_found", "Resource not found")

    def do_POST(self) -> None:  # noqa: N802
        path = self.path.split("?", 1)[0]
        if path in {"/__test/reset", "/__test/scenario"}:
            self._test_control(path)
            return
        if path in {"/webhooks/receiver", "/webhooks/fail"}:
            self._receive_webhook(path)
            return
        if path not in {"/v1/responses", "/v1/chat/completions", "/v1/embeddings"}:
            self._error(HTTPStatus.NOT_FOUND, "not_found", "Resource not found")
            return
        if not self._authorized():
            self._error(HTTPStatus.UNAUTHORIZED, "invalid_api_key", "Invalid API key")
            return
        body = self._body()
        if body is None:
            return
        if not body.get("model"):
            self._error(
                HTTPStatus.BAD_REQUEST,
                "model_required",
                "The model field is required",
            )
            return
        if self._record_and_apply_scenario(path, body):
            return
        if path == "/v1/responses":
            self._responses(body)
        elif path == "/v1/chat/completions":
            self._chat(body)
        else:
            self._embeddings(body)

    def _test_control(self, path: str) -> None:
        if not self._test_authorized():
            self._error(
                HTTPStatus.UNAUTHORIZED,
                "invalid_test_token",
                "Invalid test token",
            )
            return
        body = self._body()
        if body is None:
            return
        with TEST_LOCK:
            if path == "/__test/reset":
                UPSTREAM_REQUESTS.clear()
                TEST_SCENARIO.clear()
            else:
                TEST_SCENARIO.clear()
                TEST_SCENARIO.update(body)
        self._json(HTTPStatus.OK, {"ok": True})

    def _record_and_apply_scenario(
        self, path: str, body: dict[str, Any]
    ) -> bool:
        with TEST_LOCK:
            UPSTREAM_REQUESTS.append(
                {
                    "path": path,
                    "model": body.get("model"),
                    "stream": bool(body.get("stream", False)),
                    "client_request_id": self.headers.get(
                        "X-Client-Request-Id", ""
                    ),
                    "body": body,
                }
            )
            scenario = dict(TEST_SCENARIO)
            if scenario.get("once"):
                TEST_SCENARIO.clear()
        self._active_scenario = scenario
        delay_ms = int(scenario.get("delay_ms", 0) or 0)
        if delay_ms > 0:
            time.sleep(min(delay_ms, 30_000) / 1000)
        status = int(scenario.get("status", 0) or 0)
        if status <= 0:
            return False
        headers: dict[str, str] = {}
        if scenario.get("retry_after") is not None:
            headers["Retry-After"] = str(scenario["retry_after"])
        self._json_headers(
            status,
            {
                "error": {
                    "message": "Forced mock scenario",
                    "type": "mock_error",
                    "param": None,
                    "code": f"mock_{status}",
                }
            },
            headers,
        )
        return True

    def do_DELETE(self) -> None:  # noqa: N802
        path = self.path.split("?", 1)[0]
        if path != "/webhooks/received":
            self._error(HTTPStatus.NOT_FOUND, "not_found", "Resource not found")
            return
        with WEBHOOK_LOCK:
            WEBHOOK_DELIVERIES.clear()
        self._empty(HTTPStatus.NO_CONTENT)

    def _receive_webhook(self, path: str) -> None:
        raw = self._raw_body()
        if raw is None:
            return
        try:
            payload = json.loads(raw or b"{}")
        except (UnicodeDecodeError, json.JSONDecodeError):
            self._error(
                HTTPStatus.BAD_REQUEST, "invalid_json", "Webhook body must be JSON"
            )
            return
        timestamp = self.headers.get("X-RelayDock-Timestamp", "")
        supplied = self.headers.get("X-RelayDock-Signature", "")
        secret = os.getenv("MOCK_WEBHOOK_SECRET", "mock-webhook-secret-2026")
        digest = hmac.new(
            secret.encode("utf-8"),
            timestamp.encode("utf-8") + b"." + raw,
            hashlib.sha256,
        ).hexdigest()
        valid = hmac.compare_digest(supplied, f"v1={digest}")
        delivery = {
            "event": self.headers.get("X-RelayDock-Event", ""),
            "delivery_id": self.headers.get("X-RelayDock-Delivery", ""),
            "timestamp": timestamp,
            "signature_valid": valid,
            "payload": payload,
        }
        with WEBHOOK_LOCK:
            WEBHOOK_DELIVERIES.append(delivery)
        if path == "/webhooks/fail":
            self._json(
                HTTPStatus.INTERNAL_SERVER_ERROR,
                {"error": "forced webhook failure"},
            )
            return
        if not valid:
            self._json(
                HTTPStatus.UNAUTHORIZED,
                {"error": "invalid webhook signature"},
            )
            return
        self._empty(HTTPStatus.NO_CONTENT)

    def _response_object(
        self, body: dict[str, Any], response_id: str
    ) -> dict[str, Any]:
        message_id = object_id("msg")
        text = "RelayDock mock response"
        return {
            "id": response_id,
            "object": "response",
            "created_at": now(),
            "status": "completed",
            "background": False,
            "error": None,
            "incomplete_details": None,
            "instructions": body.get("instructions"),
            "max_output_tokens": body.get("max_output_tokens"),
            "model": body["model"],
            "output": [
                {
                    "id": message_id,
                    "type": "message",
                    "status": "completed",
                    "role": "assistant",
                    "content": [
                        {
                            "type": "output_text",
                            "annotations": [],
                            "logprobs": [],
                            "text": text,
                        }
                    ],
                }
            ],
            "parallel_tool_calls": True,
            "previous_response_id": body.get("previous_response_id"),
            "reasoning": {"effort": None, "summary": None},
            "service_tier": "default",
            "store": bool(body.get("store", True)),
            "temperature": body.get("temperature", 1.0),
            "text": {"format": {"type": "text"}},
            "tool_choice": body.get("tool_choice", "auto"),
            "tools": body.get("tools", []),
            "top_p": body.get("top_p", 1.0),
            "truncation": body.get("truncation", "disabled"),
            "usage": usage(),
            "user": body.get("user"),
            "metadata": body.get("metadata", {}),
        }

    def _responses(self, body: dict[str, Any]) -> None:
        response_id = object_id("resp")
        response = self._response_object(body, response_id)
        if not body.get("stream", False):
            self._json(HTTPStatus.OK, response)
            return

        message = response["output"][0]
        part = message["content"][0]
        events = [
            (
                "response.created",
                {
                    "type": "response.created",
                    "sequence_number": 0,
                    "response": {
                        **response,
                        "status": "in_progress",
                        "output": [],
                    },
                },
            ),
            (
                "response.output_item.added",
                {
                    "type": "response.output_item.added",
                    "sequence_number": 1,
                    "output_index": 0,
                    "item": {**message, "status": "in_progress", "content": []},
                },
            ),
            (
                "response.content_part.added",
                {
                    "type": "response.content_part.added",
                    "sequence_number": 2,
                    "item_id": message["id"],
                    "output_index": 0,
                    "content_index": 0,
                    "part": {**part, "text": ""},
                },
            ),
            (
                "response.output_text.delta",
                {
                    "type": "response.output_text.delta",
                    "sequence_number": 3,
                    "item_id": message["id"],
                    "output_index": 0,
                    "content_index": 0,
                    "delta": "RelayDock ",
                },
            ),
            (
                "response.output_text.delta",
                {
                    "type": "response.output_text.delta",
                    "sequence_number": 4,
                    "item_id": message["id"],
                    "output_index": 0,
                    "content_index": 0,
                    "delta": "mock response",
                },
            ),
            (
                "response.output_text.done",
                {
                    "type": "response.output_text.done",
                    "sequence_number": 5,
                    "item_id": message["id"],
                    "output_index": 0,
                    "content_index": 0,
                    "text": part["text"],
                },
            ),
            (
                "response.content_part.done",
                {
                    "type": "response.content_part.done",
                    "sequence_number": 6,
                    "item_id": message["id"],
                    "output_index": 0,
                    "content_index": 0,
                    "part": part,
                },
            ),
            (
                "response.output_item.done",
                {
                    "type": "response.output_item.done",
                    "sequence_number": 7,
                    "output_index": 0,
                    "item": message,
                },
            ),
            (
                "response.completed",
                {
                    "type": "response.completed",
                    "sequence_number": 8,
                    "response": response,
                },
            ),
        ]
        self._headers(HTTPStatus.OK, "text/event-stream")
        chunk_delay_ms = int(
            getattr(self, "_active_scenario", {}).get("chunk_delay_ms", 0) or 0
        )
        for event_name, payload in events:
            chunk = (
                f"event: {event_name}\n"
                f"data: {json.dumps(payload, separators=(',', ':'))}\n\n"
            )
            self.wfile.write(chunk.encode("utf-8"))
            self.wfile.flush()
            if chunk_delay_ms > 0:
                time.sleep(min(chunk_delay_ms, 30_000) / 1000)
        self.close_connection = True

    def _chat(self, body: dict[str, Any]) -> None:
        completion_id = object_id("chatcmpl")
        created = now()
        model = body["model"]
        if not body.get("stream", False):
            self._json(
                HTTPStatus.OK,
                {
                    "id": completion_id,
                    "object": "chat.completion",
                    "created": created,
                    "model": model,
                    "service_tier": "default",
                    "system_fingerprint": "fp_relaydock_mock",
                    "choices": [
                        {
                            "index": 0,
                            "message": {
                                "role": "assistant",
                                "content": "RelayDock mock completion",
                                "refusal": None,
                            },
                            "logprobs": None,
                            "finish_reason": "stop",
                        }
                    ],
                    "usage": {
                        "prompt_tokens": 4,
                        "completion_tokens": 5,
                        "total_tokens": 9,
                        "prompt_tokens_details": {"cached_tokens": 0},
                        "completion_tokens_details": {"reasoning_tokens": 0},
                    },
                },
            )
            return

        chunks = [
            {"role": "assistant", "content": ""},
            {"content": "RelayDock "},
            {"content": "mock completion"},
        ]
        self._headers(HTTPStatus.OK, "text/event-stream")
        chunk_delay_ms = int(
            getattr(self, "_active_scenario", {}).get("chunk_delay_ms", 0) or 0
        )
        for delta in chunks:
            payload = {
                "id": completion_id,
                "object": "chat.completion.chunk",
                "created": created,
                "model": model,
                "system_fingerprint": "fp_relaydock_mock",
                "choices": [
                    {
                        "index": 0,
                        "delta": delta,
                        "logprobs": None,
                        "finish_reason": None,
                    }
                ],
            }
            self.wfile.write(
                f"data: {json.dumps(payload, separators=(',', ':'))}\n\n".encode(
                    "utf-8"
                )
            )
            self.wfile.flush()
            if chunk_delay_ms > 0:
                time.sleep(min(chunk_delay_ms, 30_000) / 1000)
        final = {
            "id": completion_id,
            "object": "chat.completion.chunk",
            "created": created,
            "model": model,
            "system_fingerprint": "fp_relaydock_mock",
            "choices": [
                {
                    "index": 0,
                    "delta": {},
                    "logprobs": None,
                    "finish_reason": "stop",
                }
            ],
        }
        self.wfile.write(
            (
                f"data: {json.dumps(final, separators=(',', ':'))}\n\n"
                "data: [DONE]\n\n"
            ).encode("utf-8")
        )
        self.wfile.flush()
        self.close_connection = True

    def _embeddings(self, body: dict[str, Any]) -> None:
        input_value = body.get("input", "")
        item_count = len(input_value) if isinstance(input_value, list) else 1
        data = [
            {
                "object": "embedding",
                "index": index,
                "embedding": [0.125, -0.25, 0.5, 0.0, 0.75, -0.5, 0.25, -0.125],
            }
            for index in range(item_count)
        ]
        self._json(
            HTTPStatus.OK,
            {
                "object": "list",
                "data": data,
                "model": body["model"],
                "usage": {
                    "prompt_tokens": max(1, item_count * 3),
                    "total_tokens": max(1, item_count * 3),
                },
            },
        )


def main() -> None:
    port = int(os.getenv("PORT", "8090"))
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    print(json.dumps({"service": "mock-openai", "status": "listening", "port": port}))
    server.serve_forever()


if __name__ == "__main__":
    main()
