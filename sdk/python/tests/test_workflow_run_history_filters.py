import datetime
import json
import uuid

import httpx

from faas_sdk.api.workflows import create_workflow_run, list_workflow_runs
from faas_sdk.client import AuthenticatedClient


def test_workflow_run_history_filters_are_sent_as_query_parameters():
    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/billing/workflows/runs"
        assert request.url.params["workflow_name"] == "paid-invoice"
        assert request.url.params["status"] == "failed"
        assert request.url.params["created_after"] == "2026-10-01T00:00:00+00:00"
        assert request.url.params["created_before"] == "2026-10-05T23:59:59+00:00"
        assert request.url.params["limit"] == "10"
        assert request.url.params["offset"] == "20"
        return httpx.Response(200, json={"runs": [], "total": 0})

    with AuthenticatedClient(
        base_url="https://example.test", token="test-token", httpx_args={"transport": httpx.MockTransport(respond)}
    ) as client:
        response = list_workflow_runs.sync(
            "billing",
            client=client,
            status="failed",
            workflow_name="paid-invoice",
            created_after=datetime.datetime(2026, 10, 1, tzinfo=datetime.UTC),
            created_before=datetime.datetime(2026, 10, 5, 23, 59, 59, tzinfo=datetime.UTC),
            limit=10,
            offset=20,
        )

    assert response is not None and response.total == 0


def test_workflow_run_creation_sends_optional_idempotency_key():
    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/v1/apps/billing/workflows/paid-invoice/runs"
        assert request.headers["Idempotency-Key"] == "invoice-event-42"
        assert json.loads(request.content) == {"invoice_id": "42"}
        return httpx.Response(
            201,
            json={
                "id": "10000000-0000-4000-8000-000000000001",
                "app_id": "10000000-0000-4000-8000-000000000002",
                "workflow_name": "paid-invoice",
                "status": "pending",
                "scheduled_for": "2026-10-05T12:00:00+00:00",
                "created_at": "2026-10-05T12:00:00+00:00",
                "updated_at": "2026-10-05T12:00:00+00:00",
            },
        )

    with AuthenticatedClient(
        base_url="https://example.test", token="test-token", httpx_args={"transport": httpx.MockTransport(respond)}
    ) as client:
        response = create_workflow_run.sync(
            "billing",
            "paid-invoice",
            client=client,
            body={"invoice_id": "42"},
            idempotency_key="invoice-event-42",
        )

    assert response is not None and response.id == uuid.UUID("10000000-0000-4000-8000-000000000001")
