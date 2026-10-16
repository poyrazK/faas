"""History clients preserve scope, pagination and the zero rollback pin."""

import json

import httpx

from faas_sdk.api.edge_rules import (
    get_environment_cache_policy_revision,
    list_environment_cache_policy_history,
    set_environment_cache_policy,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import EnvironmentCachePolicy, EnvironmentCachePolicyHistory, SetEnvironmentCachePolicyRequest


def test_history_page_exact_revision_and_guarded_initial_rollback():
    identity = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
    }
    initial = {**identity, "scope": "staging", "revision": 0, "inherited": True, "rules": []}
    proposal = {
        **identity,
        "request_id": "00000000-0000-4000-8000-000000000008",
        "expected_revision": 3,
        "restore_revision": 0,
        "inherited": True,
        "rules": [],
    }
    writes = []

    def respond(request):
        assert request.url.params["environment"] == "staging"
        if request.url.path.endswith("/history/0"):
            return httpx.Response(200, json=initial)
        if request.url.path.endswith("/history"):
            assert request.url.params["before_revision"] == "1"
            assert request.url.params["limit"] == "2"
            return httpx.Response(
                200,
                json={
                    **identity,
                    "scope": "staging",
                    "current_revision": 3,
                    "entries": [initial],
                },
            )
        assert request.method == "PUT"
        body = json.loads(request.content)
        assert body == proposal
        writes.append(body)
        return httpx.Response(
            200,
            json={
                **initial,
                "revision": 4,
                "updated_at": "2026-10-15T18:00:00Z",
            },
        )

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    page = list_environment_cache_policy_history.sync(
        "catalogue",
        client=client,
        environment="staging",
        before_revision=1,
        limit=2,
    )
    assert isinstance(page, EnvironmentCachePolicyHistory)
    assert page.current_revision == 3 and page.entries[0].revision == 0
    historical = get_environment_cache_policy_revision.sync("catalogue", 0, client=client, environment="staging")
    assert isinstance(historical, EnvironmentCachePolicy)
    assert historical.inherited and historical.revision == 0
    rollback = SetEnvironmentCachePolicyRequest.from_dict(proposal)
    for _ in range(2):
        saved = set_environment_cache_policy.sync("catalogue", client=client, environment="staging", body=rollback)
        assert isinstance(saved, EnvironmentCachePolicy)
        assert saved.inherited and saved.revision == 4
    assert writes[0] == writes[1]
