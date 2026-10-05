"""Exercise optional clone reference counts through the generated transport."""

import json

import httpx
import pytest

from faas_sdk import FaaSClient
from faas_sdk.api.projects import create_project_environment
from faas_sdk.models import CreateProjectEnvironmentRequest, ProjectEnvironmentResponse
from faas_sdk.types import UNSET


@pytest.mark.parametrize("include_reference_count", [True, False])
def test_clone_reference_count_and_older_server(include_reference_count: bool) -> None:
    calls: list[httpx.Request] = []
    clone = {
        "configuration_copied": False,
        "variables_copied": 0,
        "secrets_copied": 1,
        "workloads_copied": 1,
        "bindings_copied": 0,
        "routes_copied": 0,
        "policies_copied": 0,
        "shared_resources": [],
    }
    if include_reference_count:
        clone["secret_references_copied"] = 2

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        return httpx.Response(
            201,
            json={
                "id": "00000000-0000-0000-0000-000000000001",
                "project_id": "00000000-0000-0000-0000-000000000002",
                "slug": "preview",
                "protected": False,
                "created_at": "2026-10-01T00:00:00Z",
                "updated_at": "2026-10-01T00:00:00Z",
                "cloned_from": "production",
                "clone": clone,
            },
        )

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        cloned = create_project_environment.sync(
            "my project",
            client=client.inner,
            body=CreateProjectEnvironmentRequest(slug="preview", from_environment="production"),
        )
        assert isinstance(cloned, ProjectEnvironmentResponse)
        assert cloned.clone.secret_references_copied == (2 if include_reference_count else UNSET)
        assert cloned.clone.secrets_copied == 1
        assert ("secret_references_copied" in cloned.clone.to_dict()) == include_reference_count
        assert len(calls) == 1
        assert calls[0].url.raw_path == b"/v1/projects/my%20project/environments"
        assert json.loads(calls[0].content) == {
            "slug": "preview",
            "from_environment": "production",
            "protected": False,
            "share_resources": False,
        }
        assert calls[0].headers["Authorization"] == "Bearer token"
    finally:
        client.close()
