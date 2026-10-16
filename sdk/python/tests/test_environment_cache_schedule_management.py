"""Schedule discovery and replacement preserve cursor, target and retry identity."""

import json
from datetime import UTC, datetime
from uuid import UUID

import httpx

from faas_sdk.api.edge_rules import list_environment_cache_schedules, reschedule_environment_cache
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import (
    EnvironmentCacheSchedule,
    EnvironmentCacheScheduleList,
    RescheduleEnvironmentCacheRequest,
)


def test_schedule_discovery_and_retry_safe_replacement():
    identity = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
    }
    original_id = "00000000-0000-4000-8000-000000000004"
    successor_id = "00000000-0000-4000-8000-000000000005"
    original = {
        **identity,
        "schedule_id": original_id,
        "scope": "staging",
        "status": "scheduled",
        "execute_at": "2026-10-15T21:00:00Z",
        "created_at": "2026-10-15T20:00:00Z",
        "purge": {**identity, "request_id": original_id, "path": ""},
    }
    successor = {
        **original,
        "schedule_id": successor_id,
        "execute_at": "2026-10-15T22:00:00Z",
        "replaces_schedule_id": original_id,
        "purge": {**identity, "request_id": successor_id, "path": ""},
    }
    writes = []

    def respond(request):
        assert request.url.params["environment"] == "staging"
        if request.method == "GET":
            assert request.url.params["status"] == "scheduled"
            assert request.url.params["after"] == "opaque-cursor"
            assert request.url.params["limit"] == "1"
            assert request.url.params["from"].startswith("2026-10-15T20:00:00")
            return httpx.Response(
                200,
                json={
                    **identity,
                    "scope": "staging",
                    "entries": [original],
                    "next_cursor": "next-cursor",
                },
            )
        assert request.method == "POST"
        assert request.url.path.endswith(f"/{original_id}/reschedule")
        body = json.loads(request.content)
        assert body["replacement_id"] == successor_id
        assert body["app_id"] == identity["app_id"]
        assert "purge" not in body and "policy" not in body
        writes.append(body)
        return httpx.Response(200, json=successor)

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    page = list_environment_cache_schedules.sync(
        "catalogue",
        client=client,
        environment="staging",
        status="scheduled",
        from_=datetime(2026, 10, 15, 20, tzinfo=UTC),
        after="opaque-cursor",
        limit=1,
    )
    assert isinstance(page, EnvironmentCacheScheduleList)
    assert page.next_cursor == "next-cursor" and str(page.entries[0].schedule_id) == original_id
    body = RescheduleEnvironmentCacheRequest.from_dict(
        {
            **identity,
            "replacement_id": successor_id,
            "expected_execute_at": original["execute_at"],
            "execute_at": successor["execute_at"],
        }
    )
    for _ in range(2):
        saved = reschedule_environment_cache.sync(
            "catalogue",
            UUID(original_id),
            client=client,
            environment="staging",
            body=body,
        )
        assert isinstance(saved, EnvironmentCacheSchedule)
        assert str(saved.schedule_id) == successor_id and str(saved.replaces_schedule_id) == original_id
        assert saved.purge.path == ""
    assert writes[0] == writes[1]
