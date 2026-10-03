"""Capability clients retain optional reasons and accept older servers."""

import httpx
import pytest

from faas_sdk.api.account import get_capabilities
from faas_sdk.client import AuthenticatedClient
from faas_sdk.types import UNSET


@pytest.mark.parametrize("reason", [None, "plan_not_entitled", "runtime_unavailable"])
def test_capabilities_preserve_optional_explanations(reason):
    capability = {
        "key": "object-storage",
        "name": "Private object storage",
        "category": "data",
        "description": "Private managed buckets.",
        "maturity": "preview",
        "plans": ["hobby", "pro", "scale"],
        "docs_url": "/docs/object-storage",
        "acceptance": "provider::TestQualification",
        "enabled": False,
    }
    if reason is not None:
        capability.update(unavailable_reason=reason, unavailable_detail="Customer guidance")

    def respond(request):
        assert request.url.path == "/v1/capabilities"
        assert request.headers["authorization"] == "Bearer fixture-token"
        return httpx.Response(200, json={"registry_version": 1, "plan": "free", "capabilities": [capability]})

    client = AuthenticatedClient(
        base_url="https://api.example", token="fixture-token", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client.get_httpx_client():
        response = get_capabilities.sync(client=client)
    assert response is not None
    status = response.capabilities[0]
    assert status.enabled is False
    if reason is None:
        assert status.unavailable_reason is UNSET
        assert status.unavailable_detail is UNSET
    else:
        assert status.unavailable_reason == reason
        assert status.unavailable_detail == "Customer guidance"
    assert status.to_dict() == capability
