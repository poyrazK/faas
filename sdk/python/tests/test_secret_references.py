"""Verify environment reference names through the generated public transport."""

import json

import httpx
import pytest

from faas_sdk import FaaSClient
from faas_sdk.api.secrets import delete_app_secret_reference, list_app_secret_references, set_app_secret_reference
from faas_sdk.models import AppSecretReferenceListResponse, AppSecretReferenceResponse, PutAppSecretReferenceRequest


@pytest.mark.parametrize("environment", ["production", "a", "1", "qa", "12", "a" * 33])
def test_secret_reference_names_and_explicit_environment(environment: str) -> None:
    calls: list[httpx.Request] = []
    environment_id = "00000000-0000-0000-0000-000000000001"

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        if request.method == "DELETE":
            return httpx.Response(204)
        body = {"environment_id": environment_id, "environment": environment}
        if request.method == "PUT":
            body.update({"key": "DATABASE_URL", "reference": "secret:DATABASE"})
        else:
            body.update(
                {
                    "references": {"DATABASE_URL": "secret:DATABASE"},
                    "suppressed_keys": ["REMOVED"],
                    "count": 1,
                    "quota": 20,
                }
            )
        return httpx.Response(200, json=body)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    try:
        selected = set_app_secret_reference.sync(
            "my app",
            "DATABASE_URL",
            client=client.inner,
            environment=environment,
            body=PutAppSecretReferenceRequest(reference="secret:DATABASE"),
        )
        assert isinstance(selected, AppSecretReferenceResponse)
        assert selected.reference == "secret:DATABASE"
        assert str(selected.environment_id) == environment_id
        listed = list_app_secret_references.sync("my app", client=client.inner, environment=environment)
        assert isinstance(listed, AppSecretReferenceListResponse)
        assert listed.to_dict()["references"] == {"DATABASE_URL": "secret:DATABASE"}
        assert listed.count == 1
        assert listed.suppressed_keys == ["REMOVED"]
        removed = delete_app_secret_reference.sync_detailed(
            "my app",
            "DATABASE_URL",
            client=client.inner,
            environment=environment,
        )
        assert removed.status_code == 204
        assert [request.method for request in calls] == ["PUT", "GET", "DELETE"]
        assert json.loads(calls[0].content) == {"reference": "secret:DATABASE"}
        for request in calls:
            suffix = b"" if request.method == "GET" else b"/DATABASE_URL"
            assert (
                request.url.raw_path
                == b"/v1/apps/my%20app/secret-references" + suffix + b"?environment=" + environment.encode()
            )
            assert request.headers["Authorization"] == "Bearer token"
    finally:
        client.close()
