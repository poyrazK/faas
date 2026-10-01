import asyncio
import base64
import json
from pathlib import Path

import httpx
import pytest

from faas_sdk import (
    GREGALE_FLAG_CONTEXT_HEADER,
    GREGALE_FLAG_EVIDENCE_HEADER,
    GREGALE_FLAG_PROPAGATION_HEADER,
    AsyncGregaleFlagsTransport,
    FlagDecision,
    GregaleFlags,
    GregaleFlagsMiddleware,
    evaluate_flag,
    evaluate_variant,
    flag_bucket,
    flag_variant_bucket,
    validate_bundle,
)

CUSTOMER = "00000000-0000-0000-0000-000000000001"
APP_ID = "00000000-0000-0000-0000-000000000002"
ENVIRONMENT_ID = "00000000-0000-0000-0000-000000000003"


def make_bundle() -> dict:
    return {
        "environment_id": ENVIRONMENT_ID,
        "version": 1,
        "groups": {"internal": [CUSTOMER]},
        "flags": [
            {
                "key": "export",
                "enabled": True,
                "default": False,
                "seed": "export-seed",
                "rules": [{"id": "selected", "group": "internal", "value": True}],
            },
            {
                "key": "checkout",
                "type": "variant",
                "enabled": True,
                "default": "control",
                "seed": "checkout-seed",
                "variants": [
                    {"key": "control", "weight": 6000},
                    {"key": "treatment", "weight": 4000},
                ],
                "rules": [{"id": "eligible", "rollout": 10_000}],
            },
        ],
    }


def decode_b64url(value: str) -> bytes:
    return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))


def make_propagation(customer: str = CUSTOMER) -> str:
    payload = {
        "version": 1,
        "customer_id": CUSTOMER,
        "decisions": [
            {
                "flag": "export",
                "value": True,
                "config_version": 7,
                "rule_id": "selected",
                "reason": "rule_match",
                "source": "configuration",
                "origin": {"app_id": APP_ID, "environment_id": ENVIRONMENT_ID},
            }
        ],
    }
    payload["customer_id"] = customer
    return base64.urlsafe_b64encode(json.dumps(payload, separators=(",", ":")).encode()).decode().rstrip("=")


def test_cross_language_allocation_vectors():
    repo_root = Path(__file__).resolve().parents[3]
    for name, evaluator in (
        ("allocation.json", flag_bucket),
        ("variant_allocation.json", flag_variant_bucket),
    ):
        vectors = json.loads((repo_root / "pkg" / "flags" / "testdata" / name).read_text())
        for vector in vectors:
            assert evaluator(vector["seed"], vector["key"], vector["customer"]) == vector["bucket"]


def test_evaluation_matches_targeting_and_explanation_contract():
    bundle = make_bundle()
    decision = evaluate_flag(bundle, "export", CUSTOMER, False)
    assert decision == FlagDecision("export", True, 1, "rule_match", "configuration", rule_id="selected")
    anonymous = evaluate_flag(bundle, "export", None, True)
    assert anonymous.value is False
    assert anonymous.reason == "customer_missing"
    assert evaluate_flag(bundle, "missing", CUSTOMER, True).reason == "flag_missing"
    assert evaluate_variant(bundle, "export", CUSTOMER, "control").reason == "type_mismatch"

    variant = evaluate_variant(bundle, "checkout", CUSTOMER, "control")
    assert variant.type_ == "variant"
    assert variant.value in {"control", "treatment"}
    assert variant.rule_id == "eligible"
    assert variant.bucket == flag_variant_bucket("checkout-seed", "checkout", CUSTOMER)
    assert evaluate_variant(bundle, "checkout", CUSTOMER, "control") == variant


def test_validation_rejects_ambiguous_type_and_boolean_rollout():
    bundle = make_bundle()
    bad_type = {**bundle, "flags": [{**bundle["flags"][0], "type": None}]}
    with pytest.raises(ValueError, match="Invalid Flags definition"):
        validate_bundle(bad_type)
    bad_rollout = {
        **bundle,
        "flags": [
            {
                **bundle["flags"][0],
                "rules": [{"id": "selected", "rollout": True, "value": True}],
            }
        ],
    }
    with pytest.raises(ValueError, match="Invalid Flags rule"):
        validate_bundle(bad_rollout)


def flags_client(bundle_state: dict, calls: list[httpx.Request], *, now=None):
    offline = {"value": False}

    async def handler(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        if request.url.path == "/token":
            assert request.url.params["audience"] == "gregale:flags"
            if offline["value"]:
                raise httpx.ConnectError("offline", request=request)
            return httpx.Response(200, json={"access_token": "workload-token"})
        assert request.headers["authorization"] == "Bearer workload-token"
        if offline["value"]:
            raise httpx.ConnectError("offline", request=request)
        return httpx.Response(200, json=bundle_state["value"])

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    flags = GregaleFlags(
        "https://api.example.test",
        "http://127.0.0.1:8082/token",
        async_client=client,
        now=now,
    )
    return flags, client, offline


async def close_flags(flags: GregaleFlags, client: httpx.AsyncClient) -> None:
    await flags.close()
    await client.aclose()


@pytest.mark.asyncio
async def test_request_snapshot_evidence_refresh_and_stale_fallback():
    state = {"value": make_bundle()}
    calls: list[httpx.Request] = []
    now = {"value": 0}
    flags, client, offline = flags_client(state, calls, now=lambda: now["value"])
    try:
        await flags.refresh()
        async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER}):
            assert flags.boolean("export", False).value is True
            state["value"] = {
                **make_bundle(),
                "version": 2,
                "flags": [{**make_bundle()["flags"][0], "enabled": False}],
            }
            await flags.refresh()
            assert flags.variant("checkout", "control").config_version == 1
            flags.used("export")
            evidence = flags.evidence()
            assert evidence[0]["used"] is True
            decoded = json.loads(decode_b64url(flags.response_evidence()))
            assert decoded[0]["rule_id"] == "selected"
            assert decoded[0]["source"] == "configuration"

        async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER}):
            disabled = flags.boolean("export", True)
            assert disabled.value is False
            assert disabled.reason == "disabled"
            assert disabled.config_version == 2

        now["value"] = 120_000
        state["value"] = {**make_bundle(), "version": 3}
        async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER}):
            assert flags.boolean("export", False).config_version == 3

        offline["value"] = True
        now["value"] = 240_000
        async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER}):
            stale = flags.boolean("export", False)
            assert stale.value is False
            assert stale.reason == "configuration_stale"
            assert stale.config_version == 3
        assert len(calls) >= 7
    finally:
        await close_flags(flags, client)


@pytest.mark.asyncio
async def test_context_isolated_between_concurrent_requests_and_required_for_checks():
    state = {"value": make_bundle()}
    calls: list[httpx.Request] = []
    flags, client, _ = flags_client(state, calls)
    try:
        await flags.refresh()

        async def selected():
            async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER}):
                await asyncio.sleep(0)
                return flags.boolean("export", False).value

        async def anonymous():
            async with flags.run_request({}):
                await asyncio.sleep(0)
                decision = flags.boolean("export", True)
                return decision.value, decision.reason

        assert await asyncio.gather(selected(), anonymous()) == [
            True,
            (False, "customer_missing"),
        ]
        with pytest.raises(RuntimeError, match="require run_request"):
            flags.boolean("export", False)
    finally:
        await close_flags(flags, client)


@pytest.mark.asyncio
async def test_startup_outage_uses_fallback_and_closes_refresh_task():
    calls: list[httpx.Request] = []
    flags, client, offline = flags_client({"value": make_bundle()}, calls)
    offline["value"] = True
    try:
        await flags.start()
        async with flags.run_request({}):
            decision = flags.boolean("export", True)
            assert decision.value is True
            assert decision.reason == "configuration_stale"
    finally:
        await close_flags(flags, client)


@pytest.mark.asyncio
async def test_used_decisions_propagate_only_to_managed_services(monkeypatch):
    state = {"value": make_bundle()}
    calls: list[httpx.Request] = []
    flags, config_client, _ = flags_client(state, calls)
    seen: list[httpx.Request] = []

    async def service_handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(204)

    monkeypatch.setenv("FAAS_APP_ID", APP_ID)
    try:
        await flags.refresh()
        async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER}):
            flags.boolean("export", False)
            flags.used("export")
            flags.boolean("not-used", True)
            transport = AsyncGregaleFlagsTransport(flags, httpx.MockTransport(service_handler))
            async with httpx.AsyncClient(transport=transport) as service_client:
                await service_client.get(
                    "http://billing.svc.gregale/charge",
                    headers={GREGALE_FLAG_PROPAGATION_HEADER: "forged"},
                )
                await service_client.get(
                    "https://payments.example.test/charge",
                    headers={GREGALE_FLAG_PROPAGATION_HEADER: "forged"},
                )
        encoded = seen[0].headers[GREGALE_FLAG_PROPAGATION_HEADER]
        envelope = json.loads(decode_b64url(encoded))
        assert envelope["customer_id"] == CUSTOMER
        assert [row["flag"] for row in envelope["decisions"]] == ["export"]
        assert envelope["decisions"][0]["origin"] == {
            "app_id": APP_ID,
            "environment_id": ENVIRONMENT_ID,
        }
        assert GREGALE_FLAG_PROPAGATION_HEADER not in seen[1].headers
    finally:
        await close_flags(flags, config_client)


@pytest.mark.asyncio
async def test_inherited_decision_keeps_original_version_and_customer_scope():
    state = {"value": make_bundle()}
    state["value"]["flags"][0]["enabled"] = False
    calls: list[httpx.Request] = []
    flags, client, _ = flags_client(state, calls)
    try:
        await flags.refresh()
        async with flags.run_request(
            {
                GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER,
                GREGALE_FLAG_PROPAGATION_HEADER: make_propagation(),
            }
        ):
            inherited = flags.boolean("export", False)
            assert inherited.value is True
            assert inherited.source == "inherited"
            assert inherited.config_version == 7
            assert inherited.inherited_from == {
                "app_id": APP_ID,
                "environment_id": ENVIRONMENT_ID,
            }
            flags.used("export")
            forwarded = json.loads(decode_b64url(flags.propagation_header()))
            assert forwarded["decisions"][0]["config_version"] == 7
            assert forwarded["decisions"][0]["source"] == "configuration"
            assert forwarded["decisions"][0]["origin"]["app_id"] == APP_ID

        other_customer = "00000000-0000-0000-0000-000000000004"
        async with flags.run_request(
            {
                GREGALE_FLAG_CONTEXT_HEADER: other_customer,
                GREGALE_FLAG_PROPAGATION_HEADER: make_propagation(),
            }
        ):
            decision = flags.boolean("export", False)
            assert decision.value is False
            assert decision.source == "configuration"
    finally:
        await close_flags(flags, client)


@pytest.mark.asyncio
async def test_asgi_middleware_replaces_forged_evidence_header():
    state = {"value": make_bundle()}
    calls: list[httpx.Request] = []
    flags, client, _ = flags_client(state, calls)
    messages: list[dict] = []

    async def app(scope, receive, send):
        flags.boolean("export", False)
        flags.used("export")
        await send(
            {
                "type": "http.response.start",
                "status": 200,
                "headers": [(b"x-faas-flag-evidence", b"forged")],
            }
        )
        await send({"type": "http.response.body", "body": b"ok"})

    async def receive():
        return {"type": "http.request", "body": b"", "more_body": False}

    async def send(message):
        messages.append(message)

    middleware = GregaleFlagsMiddleware(app, flags)
    try:
        await flags.refresh()
        await middleware(
            {
                "type": "http",
                "headers": [(GREGALE_FLAG_CONTEXT_HEADER.lower().encode(), CUSTOMER.encode())],
            },
            receive,
            send,
        )
        response = messages[0]
        evidence_values = [
            value for name, value in response["headers"] if name == GREGALE_FLAG_EVIDENCE_HEADER.lower().encode()
        ]
        assert len(evidence_values) == 1
        assert json.loads(decode_b64url(evidence_values[0].decode()))[0]["used"] is True

        async def no_flag_app(scope, receive, send):
            await send(
                {
                    "type": "http.response.start",
                    "status": 200,
                    "headers": [(GREGALE_FLAG_EVIDENCE_HEADER.lower().encode(), b"forged")],
                }
            )
            await send({"type": "http.response.body", "body": b"ok"})

        messages.clear()
        await GregaleFlagsMiddleware(no_flag_app, flags)(
            {"type": "http", "headers": []},
            receive,
            send,
        )
        assert all(name != GREGALE_FLAG_EVIDENCE_HEADER.lower().encode() for name, _ in messages[0]["headers"])
    finally:
        await close_flags(flags, client)
