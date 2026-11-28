import datetime
import json

import httpx
import pytest
from faas_sdk.preview.api.runs import delete_run_attribution_binding, list_run_history, set_run_budget
from faas_sdk.preview.client import AuthenticatedClient
from faas_sdk.preview.models.run_budget_limits import RunBudgetLimits
from faas_sdk.preview.models.run_budget_policy import RunBudgetPolicy
from faas_sdk.preview.models.run_history_response import RunHistoryResponse
from faas_sdk.preview.models.set_run_budget_request import SetRunBudgetRequest


def policy_response():
    return {
        "kind": "customer",
        "ref": "customer-42",
        "revision": 3,
        "enabled": False,
        "limits": {"max_concurrent": 0},
        "usage": {
            "day": "2026-11-16",
            "active": 1,
            "invocations": 2,
            "compute_used_mb_ms": 13600,
            "compute_reserved_mb_ms": 136000,
        },
        "alerts": [],
    }


def test_run_controls_explicit_zero_revision_and_empty_delete_ack():
    requests = []

    def handler(request):
        requests.append(request)
        if request.method == "DELETE":
            return httpx.Response(204)
        return httpx.Response(200, json=policy_response())

    client = AuthenticatedClient(base_url="https://api.example.test", token="test-key").set_httpx_client(
        httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handler))
    )
    result = set_run_budget.sync(
        "customer",
        "customer-42",
        client=client,
        body=SetRunBudgetRequest(expected_revision=2, enabled=False, limits=RunBudgetLimits(max_concurrent=0)),
    )
    assert isinstance(result, RunBudgetPolicy)
    assert result.revision == 3 and result.limits.max_concurrent == 0
    assert json.loads(requests[0].content) == {
        "expected_revision": 2,
        "enabled": False,
        "limits": {"max_concurrent": 0},
    }
    response = delete_run_attribution_binding.sync_detailed(
        "api_key", "01234567-89ab-cdef-0123-456789abcdef", client=client, expected_revision=3
    )
    assert response.status_code == 204
    assert requests[1].url.params["expected_revision"] == "3"
    assert all("Idempotency-Key" not in request.headers for request in requests)


@pytest.mark.asyncio
async def test_run_history_async_filters_and_attribution():
    requests = []
    run_id = "01234567-89ab-cdef-0123-456789abcdef"

    def handler(request):
        requests.append(request)
        return httpx.Response(
            200,
            json={
                "runs": [
                    {
                        "id": run_id,
                        "status": "cancelled",
                        "runtime": "node24",
                        "created_at": "2026-11-16T00:00:00Z",
                        "elapsed_ms": 2,
                        "receipt_url": "/v1/executions/" + run_id,
                        "attribution": {"customer_ref": "customer-42", "agent_ref": "report-agent"},
                    }
                ],
                "next_cursor": "next-page",
            },
        )

    async with httpx.AsyncClient(
        base_url="https://api.example.test", transport=httpx.MockTransport(handler)
    ) as transport:
        client = AuthenticatedClient(base_url="https://api.example.test", token="test-key").set_async_httpx_client(
            transport
        )
        result = await list_run_history.asyncio(
            client=client,
            customer_ref="customer-42",
            agent_ref="report-agent",
            task_name="report",
            task_version=1,
            status="cancelled",
            cursor="opaque/page+==",
            since=datetime.datetime(2026, 11, 16, tzinfo=datetime.UTC),
        )
    assert isinstance(result, RunHistoryResponse)
    assert result.runs[0].attribution.customer_ref == "customer-42"
    assert result.next_cursor == "next-page"
    assert requests[0].url.params["cursor"] == "opaque/page+=="
    assert requests[0].url.params["task_version"] == "1"
    assert requests[0].url.params["agent_ref"] == "report-agent"
