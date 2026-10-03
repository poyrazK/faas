from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import get_route_check_history_entry, list_route_check_history
from faas_sdk.models import RouteCheckHistoryEntry, RouteCheckHistoryPage


def test_history_round_trip_and_requests():
    app = "00000000-0000-4000-8000-000000000001"
    deployment = "00000000-0000-4000-8000-000000000002"
    check_id = "00000000-0000-4000-8000-000000000003"
    at = "2026-10-02T20:00:00+00:00"
    summary = {"newly_violated": 1, "resolved": 0, "changed": 0, "unknown": 0, "removed": 0, "observed": 0}
    page = {
        "app_id": app,
        "deployment_id": deployment,
        "entries": [
            {
                "version": 1,
                "id": check_id,
                "checked_at": at,
                "status": "violated",
                "requirements_revision": 2,
                "requirements_sha256": "a" * 64,
                "comparison_status": "comparable",
                "summary": summary,
            }
        ],
        "next_cursor": check_id,
    }
    finding = {
        "requirement": "budget",
        "status": "violated",
        "code": "budget_exceeds_maximum",
        "expected": "max_ms=500",
        "actual": "budget_ms=2000",
        "reason": "configured budget exceeds requirement",
    }
    entry = {
        "version": 1,
        "id": check_id,
        "checked_at": at,
        "check": {
            "version": 1,
            "app": "demo",
            "app_id": app,
            "deployment_id": deployment,
            "requirements_revision": 2,
            "requirements_sha256": "a" * 64,
            "configuration_sha256": "b" * 64,
            "report": {
                "version": 2,
                "sha256": "a" * 64,
                "policy_scope": "current_app",
                "status": "violated",
                "scope": "captured_contract",
                "routes": [{"method": "POST", "path": "/billing/{id}", "status": "violated", "checks": [finding]}],
            },
        },
        "changes": {
            "version": 1,
            "check_id": check_id,
            "status": "comparable",
            "summary": summary,
            "truncated": False,
            "findings": [
                {
                    "method": "POST",
                    "path": "/billing/{id}",
                    "requirement": "budget",
                    "kind": "newly_violated",
                    "after": finding,
                }
            ],
        },
    }
    assert RouteCheckHistoryPage.from_dict(page).to_dict() == page
    assert RouteCheckHistoryEntry.from_dict(entry).to_dict() == entry

    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.headers["Authorization"] == "Bearer token"
        base = f"/v1/apps/demo/route-requirements/checks/{deployment}/history"
        if request.url.path == base:
            assert request.url.params["limit"] == "3"
            assert request.url.params["before"] == check_id
            return httpx.Response(200, json=page)
        assert request.url.path == f"{base}/{check_id}"
        return httpx.Response(200, json=entry)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    got_page = list_route_check_history.sync(
        "demo", UUID(deployment), client=client.inner, limit=3, before=UUID(check_id)
    )
    assert isinstance(got_page, RouteCheckHistoryPage)
    assert got_page.entries[0].summary.newly_violated == 1
    got_entry = get_route_check_history_entry.sync("demo", UUID(deployment), UUID(check_id), client=client.inner)
    assert isinstance(got_entry, RouteCheckHistoryEntry)
    assert got_entry.changes.findings[0].after.actual == "budget_ms=2000"
    client.httpx_client.close()
