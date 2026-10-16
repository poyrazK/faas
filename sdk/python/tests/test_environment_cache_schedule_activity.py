"""Activity SDK preserves tail checkpoints and compact failure/execution metadata."""

import httpx

from faas_sdk.api.edge_rules import get_environment_cache_schedule_activity
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import EnvironmentCacheScheduleActivity


def test_activity_pages_and_empty_tail_checkpoint():
    identity = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
        "scope": "staging",
    }
    first_id = "00000000-0000-4000-8000-000000000004"
    second_id = "00000000-0000-4000-8000-000000000005"
    common = {
        "execute_at": "2026-10-15T21:00:00Z",
        "occurred_at": "2026-10-15T20:00:00Z",
    }
    requested = []

    def respond(request):
        assert request.method == "GET" and request.url.path.endswith("/cache/schedules/activity")
        assert request.url.params["environment"] == "staging" and request.url.params["limit"] == "2"
        after = request.url.params.get("after", "")
        requested.append(after)
        if not after:
            return httpx.Response(
                200,
                json={
                    **identity,
                    "cursor": "checkpoint-2",
                    "has_more": True,
                    "entries": [
                        {
                            **common,
                            "sequence": 1,
                            "schedule_id": first_id,
                            "kind": "accepted",
                            "status": "scheduled",
                            "action": "inheritance",
                        },
                        {
                            **common,
                            "sequence": 2,
                            "schedule_id": first_id,
                            "kind": "failed",
                            "status": "failed",
                            "action": "inheritance",
                            "error_code": "policy_or_target_changed",
                        },
                    ],
                },
            )
        if after == "checkpoint-2":
            return httpx.Response(
                200,
                json={
                    **identity,
                    "cursor": "checkpoint-4",
                    "has_more": False,
                    "entries": [
                        {
                            **common,
                            "sequence": 3,
                            "schedule_id": second_id,
                            "kind": "accepted",
                            "status": "scheduled",
                            "action": "purge",
                        },
                        {
                            **common,
                            "sequence": 4,
                            "schedule_id": second_id,
                            "kind": "executed",
                            "status": "executed",
                            "action": "purge",
                            "purge_generation": 7,
                        },
                    ],
                },
            )
        assert after == "checkpoint-4"
        return httpx.Response(200, json={**identity, "cursor": after, "has_more": False, "entries": []})

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    first = get_environment_cache_schedule_activity.sync("catalogue", client=client, environment="staging", limit=2)
    assert isinstance(first, EnvironmentCacheScheduleActivity) and first.has_more
    assert first.entries[1].kind == "failed" and first.entries[1].error_code == "policy_or_target_changed"
    second = get_environment_cache_schedule_activity.sync(
        "catalogue", client=client, environment="staging", after=first.cursor, limit=2
    )
    assert isinstance(second, EnvironmentCacheScheduleActivity) and not second.has_more
    assert second.entries[1].kind == "executed" and second.entries[1].purge_generation == 7
    for _ in range(2):
        tail = get_environment_cache_schedule_activity.sync(
            "catalogue", client=client, environment="staging", after=second.cursor, limit=2
        )
        assert isinstance(tail, EnvironmentCacheScheduleActivity)
        assert not tail.has_more and tail.entries == [] and tail.cursor == second.cursor
    assert requested == ["", "checkpoint-2", "checkpoint-4", "checkpoint-4"]


def test_expired_checkpoint_preserves_typed_recovery():
    from faas_sdk.models import EnvironmentCacheActivityRecovery, Problem

    recovery = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
        "scope": "staging",
        "oldest_cursor": "retained-prefix",
        "restart_cursor": "head-before-baseline",
        "retention_seconds": 2592000,
    }

    def respond(request):
        assert request.url.params["after"] == "expired-checkpoint"
        return httpx.Response(
            410,
            json={
                "title": "Activity cursor expired",
                "status": 410,
                "code": "cursor_expired",
                "activity_recovery": recovery,
            },
        )

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    response = get_environment_cache_schedule_activity.sync_detailed(
        "catalogue",
        client=client,
        environment="staging",
        after="expired-checkpoint",
    )
    assert response.status_code == 410 and isinstance(response.parsed, Problem)
    assert response.parsed.code == "cursor_expired"
    assert isinstance(response.parsed.activity_recovery, EnvironmentCacheActivityRecovery)
    assert response.parsed.activity_recovery.to_dict() == recovery
