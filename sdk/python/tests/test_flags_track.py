"""ADR 20261115163113 §2: track() records conversion events for the request's
verified customer and sends them in batches to /v1/runtime/flags/events."""

import json
from datetime import UTC, datetime

import httpx
import pytest
from faas_sdk.preview.flags import GREGALE_FLAG_CONTEXT_HEADER, GregaleFlags

TENANT = "00000000-0000-4000-8000-000000000001"
NOW_MS = datetime(2026, 11, 15, 12, tzinfo=UTC).timestamp() * 1000


def flags_client(status: int = 200, accepted: int | None = None) -> tuple[GregaleFlags, list[dict]]:
    sent: list[dict] = []

    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.host == "127.0.0.1":
            return httpx.Response(200, json={"access_token": "workload-token"})
        body = json.loads(request.content)
        sent.append(
            {
                "url": str(request.url),
                "body": body,
                "capabilities": request.headers.get("x-faas-flags-capabilities", ""),
            }
        )
        return httpx.Response(status, json={"accepted": len(body["events"]) if accepted is None else accepted})

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    flags = GregaleFlags(
        "https://api.example.test", "http://127.0.0.1:8082/token", async_client=client, now=lambda: NOW_MS
    )
    return flags, sent


@pytest.mark.asyncio
async def test_track_sends_customer_name_value_and_time():
    flags, sent = flags_client()
    async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: TENANT}):
        assert flags.track("order_completed", 1200)
        assert flags.track("signup")
    await flags.flush()
    assert len(sent) == 1
    assert sent[0]["url"] == "https://api.example.test/v1/runtime/flags/events"
    assert "track-v1" in sent[0]["capabilities"]
    assert sent[0]["body"]["events"] == [
        {"name": "order_completed", "customer_id": TENANT, "at": "2026-11-15T12:00:00Z", "value": 1200},
        {"name": "signup", "customer_id": TENANT, "at": "2026-11-15T12:00:00Z"},
    ]
    assert flags.dropped_events() == 0


@pytest.mark.asyncio
async def test_track_sends_properties_subject_and_request_flags():
    """ADR 20261117084505 §1: events carry properties, the request's subject
    and every flag decision the request made before track()."""
    flags, sent = flags_client()
    async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: TENANT}):
        async with flags.for_subject("user-1"):
            flags.boolean("new-checkout", False)
            flags.variant("pricing", "control")
            assert flags.track("order_completed", properties={"plan": "pro"})
            assert flags.track("signup", subject="user-2")
            assert not flags.track("signup", properties={"Bad Key": "x"})
            assert not flags.track("signup", properties={f"k{i}": "v" for i in range(11)})
            assert not flags.track("signup", properties={"plan": "x" * 257})
    await flags.flush()
    assert "track-v2" in sent[0]["capabilities"]
    decisions = {"new-checkout": "false", "pricing": "control"}
    assert sent[0]["body"]["events"] == [
        {
            "name": "order_completed",
            "customer_id": TENANT,
            "at": "2026-11-15T12:00:00Z",
            "subject": "user-1",
            "properties": {"plan": "pro"},
            "flags": decisions,
        },
        {
            "name": "signup",
            "customer_id": TENANT,
            "at": "2026-11-15T12:00:00Z",
            "subject": "user-2",
            "flags": decisions,
        },
    ]
    assert flags.dropped_events() == 3


@pytest.mark.asyncio
async def test_track_drops_without_verified_customer_or_valid_input():
    flags, sent = flags_client()
    assert not flags.track("signup")  # outside run_request
    async with flags.run_request({}):
        assert not flags.track("signup")  # no verified customer
    async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: TENANT}):
        assert not flags.track("Bad Name")
        assert not flags.track("signup", 1.5)  # type: ignore[arg-type]
        assert not flags.track("signup", 2 * 10**12)
        assert not flags.track("signup", True)  # booleans are not values
    await flags.flush()
    assert sent == []
    assert flags.dropped_events() == 6


@pytest.mark.asyncio
async def test_flush_batches_and_counts_refused_events():
    flags, sent = flags_client(accepted=90)
    async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: TENANT}):
        for _ in range(150):
            flags.track("page_view")
    await flags.flush()
    assert [len(s["body"]["events"]) for s in sent] == [100, 50]
    assert flags.dropped_events() == 10  # 90 of 100 accepted; all 50 of the second batch

    down, _ = flags_client(status=503)
    async with down.run_request({GREGALE_FLAG_CONTEXT_HEADER: TENANT}):
        down.track("signup")
    await down.flush()
    assert down.dropped_events() == 1
