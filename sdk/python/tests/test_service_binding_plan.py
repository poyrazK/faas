import json

import httpx

from faas_sdk.preview.api.apps import update_app
from faas_sdk.client import AuthenticatedClient
from faas_sdk.preview.models.problem import Problem
from faas_sdk.preview.models.update_app_request import UpdateAppRequest


def test_guard_is_optional_and_preserves_explicit_resets():
    assert "expected_service_binding_fingerprint" not in UpdateAppRequest().to_dict()
    request = UpdateAppRequest.from_dict(
        {"expected_service_binding_fingerprint": "a" * 64, "allowed_service_callers": None}
    )
    assert request.expected_service_binding_fingerprint == "a" * 64
    assert request.to_dict()["allowed_service_callers"] is None
    assert request.to_dict()["expected_service_binding_fingerprint"] == "a" * 64


def test_conflict_does_not_retry_without_guard():
    calls = []

    def handle(request):
        calls.append(request)
        assert request.method == "PATCH"
        assert json.loads(request.content)["expected_service_binding_fingerprint"] == "a" * 64
        return httpx.Response(
            409,
            json={"type": "about:blank", "title": "Service binding plan is stale", "status": 409, "code": "conflict"},
        )

    client = AuthenticatedClient(base_url="https://api.invalid", token="fixture")
    client.set_httpx_client(httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)))
    response = update_app.sync_detailed(
        "demo", client=client, body=UpdateAppRequest(expected_service_binding_fingerprint="a" * 64)
    )
    assert response.status_code == 409
    assert isinstance(response.parsed, Problem)
    assert len(calls) == 1
