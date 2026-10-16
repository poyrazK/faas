"""Environment cache clients preserve immutable identity and retry intent."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.edge_rules import get_environment_cache_policy, set_environment_cache_policy
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import EnvironmentCachePolicy, SetEnvironmentCachePolicyRequest


def test_environment_cache_policy_read_and_guarded_replacement():
    identity = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
    }
    operation = "00000000-0000-4000-8000-000000000004"
    writes = []

    def respond(request):
        assert request.url.params == httpx.QueryParams({"environment": "staging"})
        if request.method == "GET":
            return httpx.Response(
                200, json={**identity, "scope": "staging", "revision": 0, "inherited": True, "rules": []}
            )
        body = json.loads(request.content)
        assert body == {**identity, "request_id": operation, "expected_revision": 0, "rules": []}
        writes.append(body)
        return httpx.Response(
            200,
            json={
                **identity,
                "scope": "staging",
                "revision": 1,
                "inherited": False,
                "rules": [],
                "updated_at": "2026-10-15T14:00:00Z",
            },
        )

    client = AuthenticatedClient(
        base_url="https://example.test", token="fixture", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    initial = get_environment_cache_policy.sync("catalogue", client=client, environment="staging")
    assert isinstance(initial, EnvironmentCachePolicy)
    assert initial.environment_id == UUID(identity["environment_id"])
    assert initial.inherited and initial.revision == 0
    replacement = SetEnvironmentCachePolicyRequest.from_dict(
        {**identity, "request_id": operation, "expected_revision": 0, "rules": []}
    )
    for _ in range(2):
        saved = set_environment_cache_policy.sync("catalogue", client=client, environment="staging", body=replacement)
        assert isinstance(saved, EnvironmentCachePolicy)
        assert not saved.inherited and saved.revision == 1 and saved.rules == []
    assert writes[0] == writes[1]


def test_restore_inheritance_preserves_mode_and_retry_identity():
    identity = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
    }
    proposal = {
        **identity,
        "request_id": "00000000-0000-4000-8000-000000000005",
        "expected_revision": 2,
        "inherited": True,
        "rules": [],
    }
    writes = []

    def respond(request):
        assert request.method == "PUT"
        assert request.url.params == httpx.QueryParams({"environment": "staging"})
        body = json.loads(request.content)
        assert body == proposal
        writes.append(body)
        return httpx.Response(
            200,
            json={
                **identity,
                "scope": "staging",
                "revision": 3,
                "inherited": True,
                "rules": [],
                "updated_at": "2026-10-15T16:00:00Z",
            },
        )

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    restore = SetEnvironmentCachePolicyRequest.from_dict(proposal)
    for _ in range(2):
        saved = set_environment_cache_policy.sync("catalogue", client=client, environment="staging", body=restore)
        assert isinstance(saved, EnvironmentCachePolicy)
        assert saved.inherited and saved.revision == 3 and saved.rules == []
    assert writes[0] == writes[1]


def test_copy_preserves_source_pin_destination_guard_and_retry_body():
    identity = {
        "app_id": "00000000-0000-4000-8000-000000000001",
        "project_id": "00000000-0000-4000-8000-000000000002",
        "environment_id": "00000000-0000-4000-8000-000000000003",
    }
    proposal = {
        **identity,
        "request_id": "00000000-0000-4000-8000-000000000006",
        "expected_revision": 4,
        "copy_from": {
            "scope": "staging",
            "environment_id": "00000000-0000-4000-8000-000000000007",
            "revision": 3,
        },
        "rules": [],
    }
    writes = []

    def respond(request):
        assert request.method == "PUT"
        assert request.url.params == httpx.QueryParams({"environment": "production"})
        body = json.loads(request.content)
        assert body == proposal
        writes.append(body)
        return httpx.Response(
            200,
            json={
                **identity,
                "scope": "production",
                "revision": 5,
                "inherited": False,
                "rules": [],
                "updated_at": "2026-10-15T17:00:00Z",
            },
        )

    client = AuthenticatedClient(
        base_url="https://example.test",
        token="fixture",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    copy = SetEnvironmentCachePolicyRequest.from_dict(proposal)
    for _ in range(2):
        saved = set_environment_cache_policy.sync("catalogue", client=client, environment="production", body=copy)
        assert isinstance(saved, EnvironmentCachePolicy)
        assert not saved.inherited and saved.revision == 5 and saved.rules == []
    assert writes[0] == writes[1]
