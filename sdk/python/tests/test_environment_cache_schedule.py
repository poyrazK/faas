"""Schedule SDK preserves explicit purge scope, immutable identity and timestamp."""

import json

import httpx

from faas_sdk.api.edge_rules import (
    cancel_environment_cache_schedule,
    create_environment_cache_schedule,
    get_environment_cache_schedule,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import CreateEnvironmentCacheScheduleRequest, EnvironmentCacheSchedule


def test_environment_cache_schedule_retry_read_cancel():
    identity = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
    }
    schedule_id = "00000000-0000-4000-8000-000000000004"
    proposal = {
        **identity,
        "schedule_id": schedule_id,
        "execute_at": "2026-10-15T20:00:00Z",
        "purge": {**identity, "request_id": schedule_id, "path": ""},
    }
    receipt = {
        **proposal,
        "scope": "staging",
        "status": "scheduled",
        "created_at": "2026-10-15T19:00:00Z",
    }
    writes = []

    def respond(request):
        assert request.url.params == httpx.QueryParams({"environment": "staging"})
        if request.method == "POST":
            body = json.loads(request.content)
            assert body["purge"]["path"] == ""
            assert body["schedule_id"] == schedule_id
            assert body["execute_at"] in (proposal["execute_at"], "2026-10-15T20:00:00+00:00")
            writes.append(body)
            return httpx.Response(200, json=receipt)
        assert request.url.path.endswith(schedule_id)
        if request.method == "DELETE":
            return httpx.Response(
                200,
                json={
                    **receipt,
                    "status": "cancelled",
                    "finished_at": "2026-10-15T19:30:00Z",
                },
            )
        assert request.method == "GET"
        return httpx.Response(200, json=receipt)

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    body = CreateEnvironmentCacheScheduleRequest.from_dict(proposal)
    for _ in range(2):
        saved = create_environment_cache_schedule.sync(
            "catalogue",
            client=client,
            environment="staging",
            body=body,
        )
        assert isinstance(saved, EnvironmentCacheSchedule)
        assert saved.status == "scheduled" and saved.purge.path == ""
    assert writes[0] == writes[1]
    saved = get_environment_cache_schedule.sync(
        "catalogue",
        schedule_id,
        client=client,
        environment="staging",
    )
    assert isinstance(saved, EnvironmentCacheSchedule) and saved.status == "scheduled"
    cancelled = cancel_environment_cache_schedule.sync(
        "catalogue",
        schedule_id,
        client=client,
        environment="staging",
    )
    assert isinstance(cancelled, EnvironmentCacheSchedule) and cancelled.status == "cancelled"
    assert cancelled.finished_at is not None
