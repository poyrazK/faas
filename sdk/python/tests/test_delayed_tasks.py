"""Typed delayed-task contract coverage for the generated Python SDK."""

from __future__ import annotations

import datetime
import uuid

from faas_sdk.api.delayed_tasks import list_delayed_tasks
from faas_sdk.models import (
    DelayedTaskAfterRequest,
    DelayedTaskAfterRequestHeaders,
    DelayedTaskAfterRequestPayload,
    DelayedTaskAtRequest,
    InvocationDestinations,
    RetryPolicyDTO,
)


def test_relative_delayed_task_request_serializes_full_envelope() -> None:
    """Relative scheduling and the shared invocation options stay typed."""
    payload = DelayedTaskAfterRequestPayload()
    payload["invoice_id"] = "inv_123"
    headers = DelayedTaskAfterRequestHeaders()
    headers["X-Correlation-ID"] = "corr-123"
    success = uuid.UUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")

    request = DelayedTaskAfterRequest(
        delay_seconds=30 * 60,
        payload=payload,
        headers=headers,
        method="PUT",
        path="/internal/send-reminder",
        retry_policy=RetryPolicyDTO(max_attempts=4, base_seconds=1.5),
        retention_seconds=3600,
        destinations=InvocationDestinations(on_success=success),
    )

    assert request.to_dict() == {
        "delay_seconds": 1800,
        "payload": {"invoice_id": "inv_123"},
        "headers": {"X-Correlation-ID": "corr-123"},
        "method": "PUT",
        "path": "/internal/send-reminder",
        "retry_policy": {"max_attempts": 4, "base_seconds": 1.5},
        "retention_seconds": 3600,
        "destinations": {"on_success": str(success)},
    }


def test_absolute_and_list_surfaces_are_generated() -> None:
    """The absolute variant and app-scoped list route remain discoverable."""
    when = datetime.datetime(2026, 10, 1, 9, tzinfo=datetime.UTC)
    assert DelayedTaskAtRequest(scheduled_at=when).to_dict() == {
        "scheduled_at": "2026-10-01T09:00:00+00:00",
        "method": "POST",
        "path": "/",
    }

    kwargs = list_delayed_tasks._get_kwargs("billing", before="abc", limit=25)
    assert kwargs["method"] == "get"
    assert kwargs["url"] == "/v1/apps/billing/delayed-tasks"
    assert kwargs["params"] == {"before": "abc", "limit": 25}
