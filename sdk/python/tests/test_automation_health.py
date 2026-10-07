import datetime

import httpx
import pytest

from faas_sdk.api.workflows import get_automation_health
from faas_sdk.client import AuthenticatedClient
from faas_sdk.types import UNSET


@pytest.mark.parametrize("with_queue", [True, False])
def test_automation_health_window_and_current_or_legacy_queue(with_queue):
    queue = {
        "observed_at": "2026-10-07T12:00:00+00:00",
        "waiting_run_count": 3,
        "due_run_count": 2,
        "stale_run_count": 1,
        "oldest_due_age_seconds": 42.5,
        "app_running_count": 2,
        "app_dispatch_limit": 2,
        "tenant_dispatch_limit": 1,
        "app_at_capacity": True,
        "reason_counts": {
            "ready": 0,
            "scheduled": 0,
            "retry_backoff": 0,
            "parked_wait": 1,
            "app_capacity": 2,
            "tenant_capacity": 0,
            "workflow_capacity": 0,
        },
    }

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
                **({"queue": queue} if with_queue else {}),
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
    if with_queue:
        assert result.queue is not UNSET
        assert result.queue.to_dict() == queue
        assert result.queue.reason_counts.app_capacity == 2
        assert result.to_dict()["queue"] == queue
    else:
        assert result.queue is UNSET and "queue" not in result.to_dict()
