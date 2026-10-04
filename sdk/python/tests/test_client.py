"""test_client — unit tests for the BaseTransport chain + sentinels.

No fakeapid; these tests run an in-process `httpx.MockTransport` and
assert:

* `Rfc7807Layer` decodes Problem bodies and raises the canonical
  sentinel (`ErrNotFound`, `ErrUnauthorized`, `ErrRateLimited`,
  `ErrCapacity`).
* `IdempotencyTransport` mints a UUIDv4 on mutating calls and
  preserves an explicit `Idempotency-Key` header.
* `RetryTransport` retries 5xx and 429, gives up after
  `max_attempts`, and respects the configured `backoff`.
* `LoggingTransport` emits a log line per request + response when a
  logger is supplied, and is identity when `logger=None`.
"""

from __future__ import annotations

import logging
import re
import time
import uuid
from typing import Any

import httpx
import pytest
from _constants import STABLE_IDEMPOTENCY_KEY

from faas_sdk import (
    ErrCapacity,
    ErrNotFound,
    ErrRateLimited,
    ErrUnauthorized,
    FaasProblemError,
    Problem,
    is_faas_error,
)
from faas_sdk._rfc7807 import parse_problem
from faas_sdk._transport import (
    MUTATING_METHODS,
    AsyncIdempotencyTransport,
    AsyncRfc7807Layer,
    IdempotencyTransport,
    LoggingTransport,
    RetryOptions,
    RetryTransport,
    Rfc7807Layer,
    WrapperOptions,
    build_async_chain,
    build_chain,
)
from faas_sdk.idempotency import (
    HEADER_NAME,
    current_idempotency_key,
    mint_idempotency_key,
    with_idempotency_key,
)

_UUID4 = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")


# ---------------------------------------------------------------------------
# Sentinels + RFC 7807 decoder
# ---------------------------------------------------------------------------


def test_parse_problem_decodes_canonical_body() -> None:
    body = (
        b'{"type":"https://docs.example.com/not-found",'
        b'"title":"app not found","status":404,"detail":"no such app",'
        b'"code":"not_found","tx_id":"abc-123"}'
    )
    problem = parse_problem(body, "application/problem+json", 404)
    assert problem is not None
    assert problem.status == 404
    assert problem.title == "app not found"
    assert problem.detail == "no such app"
    assert problem.code == "not_found"
    assert problem.tx_id == "abc-123"


def test_parse_problem_rejects_non_problem_body() -> None:
    assert parse_problem(b"plain text", "text/plain", 500) is None
    assert parse_problem(b"", "application/problem+json", 500) is None
    assert parse_problem(b"[]", "application/json", 500) is None


def test_parse_problem_decodes_provider_neutral_checkout_url() -> None:
    body = (
        b'{"type":"about:blank","title":"payment required",'
        b'"status":402,"code":"payment_required",'
        b'"checkout_url":"https://checkout.polar.sh/session-1"}'
    )
    problem = parse_problem(body, "application/problem+json", 402)
    assert problem is not None
    assert problem.checkout_url == "https://checkout.polar.sh/session-1"


@pytest.mark.parametrize(
    "code,expected_cls",
    [
        ("not_found", ErrNotFound),
        ("unauthorized", ErrUnauthorized),
        ("rate_limited", ErrRateLimited),
        ("capacity_unavailable", ErrCapacity),
    ],
)
def test_sentinel_mapping(code: str, expected_cls: type[FaasProblemError]) -> None:
    """The four canonical codes map to the four sentinels."""
    problem = Problem(type="about:blank", title="x", status=404, code=code)
    with pytest.raises(expected_cls):
        from faas_sdk._rfc7807 import raise_for_problem

        raise_for_problem(problem)


def test_unknown_code_raises_faas_problem_error() -> None:
    problem = Problem(type="about:blank", title="x", status=500, code="something_else")
    with pytest.raises(FaasProblemError):
        from faas_sdk._rfc7807 import raise_for_problem

        raise_for_problem(problem)


def test_is_faas_error_membership() -> None:
    problem = Problem(type="about:blank", title="x", status=404, code="not_found")
    err: Any
    try:
        from faas_sdk._rfc7807 import raise_for_problem

        raise_for_problem(problem)
    except FaasProblemError as e:
        err = e
    assert is_faas_error(err) is True
    assert is_faas_error(err, ErrNotFound) is True
    assert is_faas_error(err, ErrUnauthorized) is False
    # Non-Problem exceptions always return False.
    assert is_faas_error(ValueError("x")) is False


# ---------------------------------------------------------------------------
# Idempotency
# ---------------------------------------------------------------------------


def test_idempotency_auto_mints_uuid_v4_on_post() -> None:
    captured: dict[str, str | None] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["key"] = request.headers.get(HEADER_NAME)
        return httpx.Response(200, json={"ok": True})

    transport = IdempotencyTransport(httpx.MockTransport(handler))
    transport.handle_request(httpx.Request("POST", "https://api.example.com/v1/apps"))
    key = captured["key"]
    assert key is not None
    assert _UUID4.match(key)
    assert uuid.UUID(key).version == 4


def test_idempotency_skips_get() -> None:
    captured: dict[str, str | None] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["key"] = request.headers.get(HEADER_NAME)
        return httpx.Response(200, json={"ok": True})

    transport = IdempotencyTransport(httpx.MockTransport(handler))
    transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/apps"))
    assert captured["key"] is None


@pytest.mark.parametrize("method", sorted(MUTATING_METHODS))
def test_idempotency_attaches_on_all_mutating_methods(method: str) -> None:
    captured: dict[str, str | None] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["key"] = request.headers.get(HEADER_NAME)
        return httpx.Response(200, json={"ok": True})

    transport = IdempotencyTransport(httpx.MockTransport(handler))
    transport.handle_request(httpx.Request(method, "https://api.example.com/v1/x"))
    assert captured["key"] is not None
    assert _UUID4.match(captured["key"])


def test_idempotency_preserves_explicit_header() -> None:
    captured: dict[str, str | None] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["key"] = request.headers.get(HEADER_NAME)
        return httpx.Response(200, json={"ok": True})

    transport = IdempotencyTransport(httpx.MockTransport(handler))
    request = httpx.Request(
        "POST",
        "https://api.example.com/v1/apps",
        headers={HEADER_NAME: STABLE_IDEMPOTENCY_KEY},
    )
    transport.handle_request(request)
    assert captured["key"] == STABLE_IDEMPOTENCY_KEY


def test_with_idempotency_key_overrides_auto_mint() -> None:
    """The ContextVar set by `with_idempotency_key(...)` takes
    precedence over auto-mint for the duration of the `with` block.
    """
    captured: dict[str, str | None] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["key"] = request.headers.get(HEADER_NAME)
        return httpx.Response(200, json={"ok": True})

    transport = IdempotencyTransport(httpx.MockTransport(handler))

    # Outside the `with`: auto-mint.
    transport.handle_request(httpx.Request("POST", "https://api.example.com/v1/x"))
    assert _UUID4.match(captured["key"])

    # Inside the `with`: explicit.
    with with_idempotency_key(STABLE_IDEMPOTENCY_KEY):
        transport.handle_request(httpx.Request("POST", "https://api.example.com/v1/x"))
        assert captured["key"] == STABLE_IDEMPOTENCY_KEY
        # Current key is the explicit one.
        assert current_idempotency_key() == STABLE_IDEMPOTENCY_KEY

    # Outside again: back to auto-mint.
    transport.handle_request(httpx.Request("POST", "https://api.example.com/v1/x"))
    assert _UUID4.match(captured["key"])
    assert current_idempotency_key() is None


def test_mint_idempotency_key_is_uuid_v4() -> None:
    key = mint_idempotency_key()
    assert _UUID4.match(key)
    assert uuid.UUID(key).version == 4


# ---------------------------------------------------------------------------
# Retry
# ---------------------------------------------------------------------------


def test_retry_retries_on_5xx_until_success() -> None:
    attempts: list[int] = []

    def handler(request: httpx.Request) -> httpx.Response:
        attempts.append(len(attempts) + 1)
        if len(attempts) < 3:
            return httpx.Response(500)
        return httpx.Response(200, json={"ok": True})

    transport = RetryTransport(httpx.MockTransport(handler), max_attempts=3, backoff=0.0)
    response = transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert response.status_code == 200
    assert len(attempts) == 3


def test_retry_retries_on_429() -> None:
    attempts: list[int] = []

    def handler(request: httpx.Request) -> httpx.Response:
        attempts.append(len(attempts) + 1)
        if len(attempts) < 2:
            return httpx.Response(429)
        return httpx.Response(200, json={"ok": True})

    transport = RetryTransport(httpx.MockTransport(handler), max_attempts=2, backoff=0.0)
    response = transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert response.status_code == 200
    assert len(attempts) == 2


def test_retry_does_not_retry_4xx() -> None:
    attempts: list[int] = []

    def handler(request: httpx.Request) -> httpx.Response:
        attempts.append(len(attempts) + 1)
        return httpx.Response(400)

    transport = RetryTransport(httpx.MockTransport(handler), max_attempts=3, backoff=0.0)
    transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert len(attempts) == 1


def test_retry_gives_up_after_max_attempts() -> None:
    attempts: list[int] = []

    def handler(request: httpx.Request) -> httpx.Response:
        attempts.append(len(attempts) + 1)
        return httpx.Response(503)

    transport = RetryTransport(httpx.MockTransport(handler), max_attempts=2, backoff=0.0)
    response = transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert response.status_code == 503
    # First attempt + 2 retries = 3 total.
    assert len(attempts) == 3


def test_retry_max_attempts_zero_is_identity() -> None:
    """`max_attempts=0` is the documented no-op identity; the wrapped
    transport runs exactly once."""
    attempts: list[int] = []

    def handler(request: httpx.Request) -> httpx.Response:
        attempts.append(len(attempts) + 1)
        return httpx.Response(500)

    transport = RetryTransport(httpx.MockTransport(handler), max_attempts=0, backoff=0.0)
    transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert len(attempts) == 1


def test_retry_respects_retry_after_header() -> None:
    """`Retry-After: 0.05` short-circuits the backoff path; the
    transport waits the server-supplied duration. The test asserts
    the wall-clock interval is at least the server value (with
    tolerance for httpx mock overhead).
    """
    attempts: list[int] = []
    start = time.monotonic()

    def handler(request: httpx.Request) -> httpx.Response:
        attempts.append(len(attempts) + 1)
        if len(attempts) == 1:
            return httpx.Response(503, headers={"Retry-After": "0.05"})
        return httpx.Response(200, json={"ok": True})

    transport = RetryTransport(httpx.MockTransport(handler), max_attempts=2, backoff=10.0)
    response = transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    elapsed = time.monotonic() - start
    assert response.status_code == 200
    assert elapsed >= 0.04  # tight tolerance — Retry-After: 0.05


# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------


def test_logging_transport_emits_request_and_response_lines() -> None:
    log_records: list[logging.LogRecord] = []

    class _ListHandler(logging.Handler):
        def emit(self, record: logging.LogRecord) -> None:
            log_records.append(record)

    logger = logging.getLogger("faas_sdk.tests.logging")
    logger.setLevel(logging.DEBUG)
    logger.addHandler(_ListHandler())
    logger.propagate = False

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"ok": True})

    transport = LoggingTransport(httpx.MockTransport(handler), logger=logger)
    transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))

    messages = [r.getMessage() for r in log_records]
    assert "request" in messages
    assert "response" in messages


def test_logging_transport_no_logger_is_identity() -> None:
    """`logger=None` short-circuits the log call; the wrapped
    transport is called once and its response is returned as-is.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"ok": True})

    transport = LoggingTransport(httpx.MockTransport(handler), logger=None)
    response = transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert response.status_code == 200


# ---------------------------------------------------------------------------
# RFC 7807 layer
# ---------------------------------------------------------------------------


def test_rfc7807_layer_raises_canonical_sentinel() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            404,
            headers={"Content-Type": "application/problem+json"},
            content=b'{"type":"about:blank","title":"app not found","status":404,"code":"not_found"}',
        )

    transport = Rfc7807Layer(httpx.MockTransport(handler))
    with pytest.raises(ErrNotFound):
        transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/apps/missing-app-404"))


def test_rfc7807_layer_passes_through_2xx() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"ok": True})

    transport = Rfc7807Layer(httpx.MockTransport(handler))
    response = transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert response.status_code == 200


def test_rfc7807_layer_passes_through_4xx_with_non_problem_body() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(400, text="bad request")

    transport = Rfc7807Layer(httpx.MockTransport(handler))
    response = transport.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert response.status_code == 400


# ---------------------------------------------------------------------------
# Chain composition
# ---------------------------------------------------------------------------


def test_build_chain_composes_outer_to_inner() -> None:
    """`build_chain` returns a transport whose type is the outermost
    layer (`RetryTransport`). The chain's response survives a
    5xx → 200 retry sequence end-to-end (rfc7807 + idempotency +
    retry + logging all play).
    """
    attempts: list[int] = []

    def handler(request: httpx.Request) -> httpx.Response:
        attempts.append(len(attempts) + 1)
        if len(attempts) < 2:
            return httpx.Response(503)
        return httpx.Response(200, json={"ok": True})

    options = WrapperOptions(retry=RetryOptions(max_attempts=2, backoff=0.0))
    chain = build_chain(httpx.MockTransport(handler), options=options)
    assert isinstance(chain, RetryTransport)

    response = chain.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert response.status_code == 200
    assert len(attempts) == 2


def test_chain_decodes_problem_after_retry_exhaustion() -> None:
    """When the retry layer gives up, the response falls through to
    the `Rfc7807Layer`. A 5xx with a Problem body and a `code` field
    that maps to no sentinel still raises `FaasProblemError` — the
    wrapper exposes every Problem shape, not just the four canonical
    sentinels.
    """

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            500,
            headers={"Content-Type": "application/problem+json"},
            content=b'{"status":500,"title":"boom","code":"unknown_thing"}',
        )

    options = WrapperOptions(retry=RetryOptions(max_attempts=1, backoff=0.0))
    chain = build_chain(httpx.MockTransport(handler), options=options)
    with pytest.raises(FaasProblemError) as excinfo:
        chain.handle_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    # 500 + Problem body with unknown code → FaasProblemError, not a
    # canonical sentinel.
    assert excinfo.value.problem.status == 500
    assert excinfo.value.problem.code == "unknown_thing"


# ---------------------------------------------------------------------------
# Async chain — parity with sync
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_async_idempotency_auto_mints_uuid_v4_on_post() -> None:
    """Async sibling of `IdempotencyTransport` mirrors the sync
    auto-mint contract."""
    captured: dict[str, str | None] = {}

    async def handler(request: httpx.Request) -> httpx.Response:
        captured["key"] = request.headers.get(HEADER_NAME)
        return httpx.Response(200, json={"ok": True})

    transport = AsyncIdempotencyTransport(httpx.MockTransport(handler))
    await transport.handle_async_request(
        httpx.Request("POST", "https://api.example.com/v1/apps"),
    )
    key = captured["key"]
    assert key is not None
    assert _UUID4.match(key)
    assert uuid.UUID(key).version == 4


@pytest.mark.asyncio
async def test_async_idempotency_skips_get() -> None:
    """Async transport skips non-mutating methods identically to
    the sync `IdempotencyTransport`."""
    captured: dict[str, str | None] = {}

    async def handler(request: httpx.Request) -> httpx.Response:
        captured["key"] = request.headers.get(HEADER_NAME)
        return httpx.Response(200, json={"ok": True})

    transport = AsyncIdempotencyTransport(httpx.MockTransport(handler))
    await transport.handle_async_request(
        httpx.Request("GET", "https://api.example.com/v1/apps"),
    )
    assert captured["key"] is None


@pytest.mark.asyncio
async def test_async_rfc7807_layer_raises_canonical_sentinel() -> None:
    """The async RFC 7807 layer must raise `ErrNotFound` exactly as
    the sync layer does — without parity, async callers bypass
    Problem decoding (review finding 5)."""

    async def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            404,
            headers={"Content-Type": "application/problem+json"},
            content=b'{"type":"about:blank","title":"app not found","status":404,"code":"not_found"}',
        )

    transport = AsyncRfc7807Layer(httpx.MockTransport(handler))
    with pytest.raises(ErrNotFound):
        await transport.handle_async_request(httpx.Request("GET", "https://api.example.com/v1/apps/missing"))


@pytest.mark.asyncio
async def test_async_chain_decodes_problem_after_retry_exhaustion() -> None:
    """End-to-end: AsyncRetryTransport → AsyncLoggingTransport →
    AsyncRfc7807Layer → AsyncIdempotencyTransport → user transport.
    A 5xx with a Problem body and an unknown code still raises
    `FaasProblemError` once retries are exhausted.
    """

    async def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            500,
            headers={"Content-Type": "application/problem+json"},
            content=b'{"status":500,"title":"boom","code":"unknown_thing"}',
        )

    options = WrapperOptions(retry=RetryOptions(max_attempts=1, backoff=0.0))
    chain = build_async_chain(httpx.MockTransport(handler), options=options)
    with pytest.raises(FaasProblemError) as excinfo:
        await chain.handle_async_request(httpx.Request("GET", "https://api.example.com/v1/x"))
    assert excinfo.value.problem.status == 500
    assert excinfo.value.problem.code == "unknown_thing"
