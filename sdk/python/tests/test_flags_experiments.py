"""ADR 20261119173702 §1: a rule_match on a subject experiment queues one
exposure per flag, customer, subject and variant an hour; flush sends it
beside the events."""

import json
from datetime import datetime, timezone
from pathlib import Path

import httpx
import pytest

from faas_sdk.preview.flags import GREGALE_FLAG_CONTEXT_HEADER, GregaleFlags

CUSTOMER = "00000000-0000-4000-8000-000000000101"
BUNDLE = json.loads((Path(__file__).resolve().parents[3] / "pkg" / "flags" / "testdata" / "decisions.json").read_text())["bundle"]


@pytest.mark.asyncio
async def test_subject_experiment_exposures_are_deduplicated():
    sent: list[dict] = []
    clock = {"now": datetime(2026, 11, 19, 12, tzinfo=timezone.utc).timestamp() * 1000}

    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.host == "127.0.0.1":
            return httpx.Response(200, json={"access_token": "workload-token"})
        if request.url.path == "/v1/runtime/flags":
            return httpx.Response(200, json=BUNDLE)
        sent.append(json.loads(request.content))
        return httpx.Response(200, json={"accepted": 0})

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    flags = GregaleFlags("https://api.example.test", "http://127.0.0.1:8082/token", async_client=client, now=lambda: clock["now"])
    await flags.refresh()

    async def serve() -> str:
        async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER}):
            async with flags.for_subject("user-7"):
                return flags.variant("subject-checkout", "control").value

    assert await serve() == "new"
    assert await serve() == "new"
    clock["now"] += 61 * 60 * 1000
    await serve()
    async with flags.run_request({GREGALE_FLAG_CONTEXT_HEADER: CUSTOMER}):
        flags.variant("layered-checkout", "control")
    await flags.flush()
    exposures = [e for body in sent for e in body.get("exposures", [])]
    assert len(exposures) == 2, "one exposure an hour, none for customer experiments"
    assert exposures[0] == {"flag": "subject-checkout", "rule_id": "by-user", "variant": "new", "customer_id": CUSTOMER, "subject": "user-7", "at": "2026-11-19T12:00:00Z"}
