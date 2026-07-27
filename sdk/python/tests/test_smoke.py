"""test_smoke — round-trip the five JSON routes through the FaaSClient.

Mirrors `sdk/node/test/smoke.test.ts` and `sdk/go/transport_e2e_test.go`.
Asserts:

* the generator's service functions are reachable through the
  wrapper's `client.inner`,
* the wrapper's `Rfc7807Layer` raises `ErrNotFound` (canonical
  sentinel) for `code: "not_found"` responses,
* the wrapper's `IdempotencyTransport` mints a UUIDv4 on mutating
  calls (no `Idempotency-Key` header set).

The generated `sync()` helpers return the parsed body (Optional[T])
directly. The HTTP status is observable via `client.httpx_client`'s
last response, but the smoke tests assert on the parsed body shape
or the raised exception (Rfc7807Layer raises before parsed).
"""

from __future__ import annotations

import re
import uuid

import httpx
import pytest
from _constants import STABLE_IDEMPOTENCY_KEY

from faas_sdk import (
    ErrNotFound,
    FaaSClient,
    is_faas_error,
)
from faas_sdk.models import CreateAppRequest, Problem

# UUIDv4 hex shape: 8-4-4-4-12 lower-case.
_UUID4 = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")


def test_healthz(fakeapid) -> None:
    """Direct `httpx.get` against `/__healthz` returns `{"ok": true}`."""
    resp = httpx.get(fakeapid.base_url + "/__healthz")
    assert resp.status_code == 200
    assert resp.json() == {"ok": True}


def test_account_round_trip(fakeapid) -> None:
    """`GET /v1/account` reaches the generated service function and
    returns a parsed `AccountResponse` with a `plan` field."""
    from faas_sdk.api.account import get_account

    client = FaaSClient(base_url=fakeapid.base_url, token="test-token")
    try:
        body = get_account.sync(client=client.inner)
        assert body is not None
        assert not isinstance(body, Problem)
        assert body.plan == "hobby"
    finally:
        client.close()


def test_create_and_list_apps(fakeapid) -> None:
    """`POST /v1/apps` echoes the slug from the request body, and
    `GET /v1/apps` returns a list with the just-created app in it.
    """
    from faas_sdk.api.apps import create_app, list_apps

    client = FaaSClient(base_url=fakeapid.base_url, token="test-token")
    try:
        created = create_app.sync(
            client=client.inner,
            body=CreateAppRequest(slug="hello-world"),
        )
        assert created is not None
        assert not isinstance(created, Problem)
        assert created.slug == "hello-world"

        listed = list_apps.sync(client=client.inner)
        assert listed is not None
        assert not isinstance(listed, Problem)
        assert len(listed) >= 1
        assert any(a.slug == "hello-world" for a in listed)
    finally:
        client.close()


def test_get_usage_returns_array(fakeapid) -> None:
    """`GET /v1/usage` returns an array (not a single struct). Memory
    `getusage-wire-shape-mismatch.md` documents the wire shape and the
    SDK must respect it.
    """
    from faas_sdk.api.usage import get_usage

    client = FaaSClient(base_url=fakeapid.base_url, token="test-token")
    try:
        body = get_usage.sync(client=client.inner)
        assert isinstance(body, list)
    finally:
        client.close()


def test_unknown_slug_raises_err_not_found(fakeapid) -> None:
    """`GET /v1/apps/missing-app-404` returns a 404 RFC 7807 body
    with `code: "not_found"`. The wrapper's `Rfc7807Layer` decodes
    it and raises `ErrNotFound` (the canonical sentinel for
    `code: "not_found"`).
    """
    from faas_sdk.api.apps import get_app

    client = FaaSClient(base_url=fakeapid.base_url, token="test-token")
    try:
        with pytest.raises(ErrNotFound) as excinfo:
            get_app.sync(client=client.inner, slug="missing-app-404")
        err = excinfo.value
        # The Problem is reachable on `err.problem`; the canonical code
        # is `not_found`; the status matches the wire.
        assert err.problem.status == 404
        assert err.problem.code == "not_found"
        assert is_faas_error(err, ErrNotFound) is True
    finally:
        client.close()


def test_idempotency_auto_mints_on_mutating_call(fakeapid) -> None:
    """`POST /v1/apps` carries an `Idempotency-Key` header that is a
    fresh UUIDv4 (the wrapper's auto-mint path). Memory mirror of
    `sdk/go/example_test.go::TestWithIdempotencyKey_AutoMintsWhenAbsent`
    (which had a real bug; the fix landed in PR 3.5 and is mirrored
    here at the smoke level).
    """
    from faas_sdk.api.apps import create_app

    captured: dict[str, str | None] = {}

    real_client = FaaSClient(base_url=fakeapid.base_url, token="test-token")
    try:
        # Capture by wrapping the innermost transport's
        # `handle_request`. The chain is Retry -> Logging -> Rfc7807
        # -> Idempotency -> HTTPTransport; the Idempotency layer has
        # already attached the `Idempotency-Key` header by the time
        # the innermost transport fires.
        innermost = real_client.httpx_client._transport
        while hasattr(innermost, "_transport"):
            innermost = innermost._transport
        original_handle = innermost.handle_request

        def capturing_handle(request: httpx.Request) -> httpx.Response:
            captured["idempotency"] = request.headers.get("Idempotency-Key")
            captured["method"] = request.method
            return original_handle(request)

        innermost.handle_request = capturing_handle  # type: ignore[method-assign]
        try:
            create_app.sync(
                client=real_client.inner,
                body=CreateAppRequest(slug="idem-test"),
            )
        finally:
            innermost.handle_request = original_handle  # type: ignore[method-assign]

        assert captured["method"] == "POST"
        key = captured["idempotency"]
        assert key is not None
        # UUIDv4 hex shape (canonical auto-mint format).
        assert _UUID4.match(key), f"Idempotency-Key {key!r} is not a UUIDv4"
        # And the key parses as a UUIDv4.
        parsed = uuid.UUID(key)
        assert parsed.version == 4
    finally:
        real_client.close()


def test_get_skips_idempotency_header(fakeapid) -> None:
    """`GET /v1/apps` does NOT carry `Idempotency-Key` (mutating-only
    contract; mirrors `pkg/api/client.go::do`'s
    `method != GET && method != HEAD` guard).
    """
    from faas_sdk.api.apps import list_apps

    real_client = FaaSClient(base_url=fakeapid.base_url, token="test-token")
    try:
        captured: dict[str, str | None] = {}
        innermost = real_client.httpx_client._transport
        while hasattr(innermost, "_transport"):
            innermost = innermost._transport
        original_handle = innermost.handle_request

        def capturing_handle(request: httpx.Request) -> httpx.Response:
            captured["idempotency"] = request.headers.get("Idempotency-Key")
            return original_handle(request)

        innermost.handle_request = capturing_handle  # type: ignore[method-assign]
        try:
            list_apps.sync(client=real_client.inner)
            assert captured["idempotency"] is None
        finally:
            innermost.handle_request = original_handle  # type: ignore[method-assign]
    finally:
        real_client.close()


def test_with_idempotency_key_scopes_explicit_key(fakeapid) -> None:
    """`with with_idempotency_key('deploy-2026-07-27-abc')` overrides
    the auto-mint for the duration of the `with` block.
    """
    from faas_sdk import with_idempotency_key
    from faas_sdk.api.apps import create_app

    client = FaaSClient(base_url=fakeapid.base_url, token="test-token")
    try:
        captured: dict[str, str | None] = {}
        innermost = client.httpx_client._transport
        while hasattr(innermost, "_transport"):
            innermost = innermost._transport
        original_handle = innermost.handle_request

        def capturing_handle(request: httpx.Request) -> httpx.Response:
            captured["idempotency"] = request.headers.get("Idempotency-Key")
            return original_handle(request)

        innermost.handle_request = capturing_handle  # type: ignore[method-assign]
        try:
            with with_idempotency_key(STABLE_IDEMPOTENCY_KEY):
                create_app.sync(
                    client=client.inner,
                    body=CreateAppRequest(slug="explicit-key"),
                )
        finally:
            innermost.handle_request = original_handle  # type: ignore[method-assign]

        assert captured["idempotency"] == STABLE_IDEMPOTENCY_KEY
    finally:
        client.close()
