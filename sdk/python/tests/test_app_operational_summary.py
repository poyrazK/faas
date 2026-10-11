import json
from pathlib import Path

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import get_app_operational_summary
from faas_sdk.models import AppOperationalSummary


def test_summary_preserves_unknown_health_blocked_recovery_and_unavailable_sources():
    payload = json.loads(
        (Path(__file__).resolve().parents[3] / "tests/fixtures/app-operational-summary.json").read_text()
    )
    calls = []

    def respond(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/demo/operational-summary"
        assert not request.url.query
        return httpx.Response(200, json=payload)

    client = FaaSClient(
        base_url="https://example.test", token="test", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client.httpx_client:
        summary = get_app_operational_summary.sync("demo", client=client.inner)
        assert isinstance(summary, AppOperationalSummary)
        assert summary.to_dict() == payload
        assert summary.monitoring.status == "unknown"
        assert summary.recovery.rollbacks[0].status == "blocked"
        assert not summary.recovery.restarts_available
        assert summary.recovery.rollbacks_truncated
        assert len(calls) == 1
