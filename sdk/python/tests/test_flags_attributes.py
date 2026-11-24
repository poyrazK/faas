"""Attribute targeting (ADR 20261114150000 §2) against the shared decision vectors."""

import json
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import (
    GREGALE_CLIENT_COUNTRY_HEADER,
    GREGALE_CLIENT_UA_FAMILY_HEADER,
    GREGALE_FLAG_CONTEXT_HEADER,
    GREGALE_FLAG_KILLS_HEADER,
    GregaleFlags,
    evaluate_flag,
    evaluate_variant,
    validate_bundle,
)

TENANT = "00000000-0000-4000-8000-000000000001"
VECTORS = json.loads(
    (Path(__file__).resolve().parents[3] / "pkg" / "flags" / "testdata" / "decisions.json").read_text()
)


def test_cross_language_decision_vectors():
    bundle = validate_bundle(VECTORS["bundle"])
    for case in VECTORS["cases"]:
        evaluate = evaluate_variant if case.get("type") == "variant" else evaluate_flag
        decision = evaluate(
            bundle,
            case["flag"],
            case.get("customer"),
            case["fallback"],
            case.get("subject"),
            case.get("attributes"),
            case.get("kills"),
        )
        assert decision.value == case["value"], case["name"]
        assert decision.reason == case["reason"], case["name"]
        assert decision.rule_id == case.get("rule_id"), case["name"]


def test_validate_bundle_rejects_bad_conditions():
    for conditions in (
        [],
        [{"attribute": "gregale.ip", "op": "in", "values": ["x"]}],
        [{"attribute": "gregale.country", "op": "ends_with", "values": ["E"]}],
        [{"attribute": "app.plan", "op": "in", "values": []}],
    ):
        bundle = json.loads(json.dumps(VECTORS["bundle"]))
        bundle["flags"][0]["rules"][0]["attributes"] = conditions
        with pytest.raises(ValueError):
            validate_bundle(bundle)


def flags_client() -> tuple[GregaleFlags, httpx.AsyncClient, dict]:
    seen: dict = {}

    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.host == "127.0.0.1":
            return httpx.Response(200, json={"access_token": "workload-token"})
        seen["capabilities"] = request.headers.get("x-faas-flags-capabilities", "")
        return httpx.Response(200, json=VECTORS["bundle"])

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    flags = GregaleFlags("https://api.example.test", "http://127.0.0.1:8082/token", async_client=client)
    return flags, client, seen


@pytest.mark.asyncio
async def test_gateway_and_application_attributes_feed_rules():
    flags, client, seen = flags_client()
    await flags.refresh()
    assert "attribute-targeting-v1" in seen["capabilities"]
    assert "kill-v1" in seen["capabilities"]
    try:
        headers = {GREGALE_FLAG_CONTEXT_HEADER: TENANT, GREGALE_CLIENT_COUNTRY_HEADER: "DE"}
        async with flags.run_request(headers):
            decision = flags.boolean("eu-checkout", False)
            assert decision.value is True and decision.rule_id == "eu"

        headers = {
            GREGALE_FLAG_CONTEXT_HEADER: TENANT,
            GREGALE_CLIENT_COUNTRY_HEADER: "DE",
            GREGALE_FLAG_KILLS_HEADER: "eu-checkout",
        }
        async with flags.run_request(headers):
            killed = flags.boolean("eu-checkout", False)
            assert killed.value is False and killed.reason == "killed"

        async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: TENANT}):
            bundled = flags.boolean("risky-path", False)
            assert bundled.value is False and bundled.reason == "killed"

        async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: TENANT, GREGALE_CLIENT_COUNTRY_HEADER: "de"}):
            assert flags.boolean("eu-checkout", False).value is False

        headers = {GREGALE_FLAG_CONTEXT_HEADER: TENANT, GREGALE_CLIENT_UA_FAMILY_HEADER: "chrome"}
        async with flags.run_request(headers):
            async with flags.for_attributes({"plan": "pro"}):
                assert flags.boolean("pro-only", False).value is True

        async with flags.run_request(headers):
            with pytest.raises(ValueError):
                async with flags.for_attributes({"Bad-Name": "x"}):
                    pass
            async with flags.for_attributes({"plan": "pro\n"}):
                assert flags.boolean("pro-only", False).value is False
    finally:
        await flags.close()
        await client.aclose()
