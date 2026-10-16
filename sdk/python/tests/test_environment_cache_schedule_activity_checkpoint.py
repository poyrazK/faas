"""The bounded checkpoint helper preserves scoped floor/head metadata."""

import httpx

from faas_sdk.api.edge_rules import get_environment_cache_schedule_activity_checkpoint
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import EnvironmentCacheActivityRecovery


def test_capture_activity_checkpoint_before_baseline():
    expected = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
        "scope": "staging",
        "oldest_cursor": "retained-floor",
        "restart_cursor": "head-before-baseline",
        "retention_seconds": 2592000,
    }

    def respond(request):
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/catalogue/cache/schedules/activity/checkpoint"
        assert dict(request.url.params) == {"environment": "staging"}
        return httpx.Response(200, json=expected)

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    point = get_environment_cache_schedule_activity_checkpoint.sync(
        "catalogue",
        client=client,
        environment="staging",
    )
    assert isinstance(point, EnvironmentCacheActivityRecovery)
    assert point.to_dict() == expected
