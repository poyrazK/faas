"""ADR 531: scoped complete queue configuration and optimistic revisions."""

import json
from http import HTTPStatus

import httpx

from faas_sdk.api.projects import (
    get_project_environment_queue_bindings,
    replace_project_environment_queue_bindings,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.problem import Problem
from faas_sdk.models.project_environment_queue_bindings_response import ProjectEnvironmentQueueBindingsResponse
from faas_sdk.models.replace_project_environment_queue_bindings_request import (
    ReplaceProjectEnvironmentQueueBindingsRequest,
)


def test_stage_queue_replacement_keeps_false_enabled_and_complete_empty() -> None:
    for definitions in [
        [],
        [
            {
                "name": "orders",
                "queue_name": "orders",
                "mode": "pull",
                "workload_class": "worker",
                "enabled": False,
                "max_concurrency": 2,
                "retry_policy": {"max_attempts": 3},
            }
        ],
    ]:
        request_body = {"expected_revision": 4, "bindings": definitions}

        def handle(request: httpx.Request) -> httpx.Response:
            assert request.method == "PUT"
            assert request.url.path == "/v1/projects/shop/environments/staging/workloads/shop-worker/queue-bindings"
            assert json.loads(request.content) == request_body
            return httpx.Response(
                200,
                json={
                    "environment": "staging",
                    "workload": "shop-worker",
                    "revision": 2,
                    "workload_revision": 5,
                    "config_hash": "a" * 64,
                    "activation_state": "unavailable",
                    "bindings": definitions,
                },
            )

        with httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handle)) as transport:
            client = AuthenticatedClient(base_url="https://api.example.test", token="token").set_httpx_client(transport)
            result = replace_project_environment_queue_bindings.sync_detailed(
                "shop",
                "staging",
                "shop-worker",
                client=client,
                body=ReplaceProjectEnvironmentQueueBindingsRequest.from_dict(request_body),
            )
        assert result.status_code == HTTPStatus.OK
        assert isinstance(result.parsed, ProjectEnvironmentQueueBindingsResponse)
        assert result.parsed.workload_revision == 5
        assert result.parsed.to_dict()["bindings"] == definitions


def test_missing_stage_queue_collection_preserves_initialization_revision() -> None:
    def handle(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.path == "/v1/projects/shop/environments/staging/workloads/shop-worker/queue-bindings"
        return httpx.Response(
            409,
            headers={"X-Gregale-Workload-Revision": "7"},
            json={
                "type": "about:blank",
                "title": "Stage queue collection unavailable",
                "status": 409,
                "code": "environment_queue_collection_unavailable",
            },
        )

    with httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.example.test", token="token").set_httpx_client(transport)
        result = get_project_environment_queue_bindings.sync_detailed("shop", "staging", "shop-worker", client=client)
    assert result.status_code == HTTPStatus.CONFLICT
    assert isinstance(result.parsed, Problem)
    assert result.parsed.code == "environment_queue_collection_unavailable"
    assert result.headers["X-Gregale-Workload-Revision"] == "7"
