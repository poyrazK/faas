import datetime

import httpx

from faas_sdk.api.workflows import get_automation_health
from faas_sdk.client import AuthenticatedClient


def test_automation_health_window_is_sent_as_query_parameters():
    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/billing/automations/paid-invoice/health"
        assert request.url.params["created_after"] == "2026-10-01T00:00:00+00:00"
        assert request.url.params["created_before"] == "2026-10-05T23:59:59+00:00"
        return httpx.Response(
            200,
            json={
                "app_slug": "billing",
                "automation_name": "paid-invoice",
                "window_start": "2026-10-01T00:00:00Z",
                "window_end": "2026-10-05T23:59:59Z",
                "run_count": 0,
                "completed_run_count": 0,
                "active_run_count": 0,
                "queued_run_count": 0,
                "success_rate": 0,
                "status_counts": {
                    "pending": 0,
                    "running": 0,
                    "awaiting_event": 0,
                    "succeeded": 0,
                    "failed": 0,
                    "dead": 0,
                },
                "failed_steps": [],
            },
        )

    with AuthenticatedClient(
        base_url="https://example.test", token="test-token", httpx_args={"transport": httpx.MockTransport(respond)}
    ) as client:
        result = get_automation_health.sync(
            "billing",
            "paid-invoice",
            client=client,
            created_after=datetime.datetime(2026, 10, 1, tzinfo=datetime.timezone.utc),
            created_before=datetime.datetime(2026, 10, 5, 23, 59, 59, tzinfo=datetime.timezone.utc),
        )

    assert result is not None and result.run_count == 0 and result.automation_name == "paid-invoice"
