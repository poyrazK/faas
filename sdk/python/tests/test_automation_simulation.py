import json

import httpx
import pytest

from faas_sdk import AuthenticatedClient
from faas_sdk.api.workflows import simulate_automation
from faas_sdk.models.automation_simulation_step import AutomationSimulationStep
from faas_sdk.models.simulate_automation_request import SimulateAutomationRequest
from faas_sdk.models.simulate_automation_response import SimulateAutomationResponse
from faas_sdk.types import UNSET

REQUEST = {
    "definition": {"name": "sample", "steps": [{"name": "a", "run": "a"}]},
    "input": {"number": 9007199254740993, "active": False},
    "mock_outputs": {"a": None},
    "mock_item_outputs": {"batch": [False, None]},
}
RESPONSE = {
    "definition_valid": True,
    "definition_hash": "hash",
    "complete": False,
    "issues": [],
    "warnings": [],
    "step_order": ["a"],
    "trace": [{"step_name": "a", "kind": "run", "state": "mocked", "output": None, "when_matched": False}],
}


def handle(request):
    assert request.method == "POST"
    assert request.url.path == "/v1/apps/billing/automations:simulate"
    assert json.loads(request.content) == REQUEST
    return httpx.Response(200, json=RESPONSE)


def assert_trace(response):
    assert isinstance(response, SimulateAutomationResponse)
    assert response.definition_valid is True
    assert response.complete is False
    assert response.trace[0].output is None
    assert response.trace[0].when_matched is False
    assert response.trace[0].input_ is UNSET
    assert response.to_dict() == RESPONSE


def test_simulation_sync_preserves_samples_and_trace():
    client = AuthenticatedClient(base_url="https://api.example.com", token="token")
    body = SimulateAutomationRequest.from_dict(REQUEST)
    assert body.to_dict() == REQUEST
    with httpx.Client(base_url="https://api.example.com", transport=httpx.MockTransport(handle)) as transport:
        client.set_httpx_client(transport)
        assert_trace(simulate_automation.sync(slug="billing", client=client, body=body))
    missing = AutomationSimulationStep.from_dict({"step_name": "a", "kind": "run", "state": "would_execute"})
    assert missing.output is UNSET
    assert "output" not in missing.to_dict()


@pytest.mark.asyncio
async def test_simulation_async_preserves_samples_and_trace():
    client = AuthenticatedClient(base_url="https://api.example.com", token="token")
    async with httpx.AsyncClient(
        base_url="https://api.example.com", transport=httpx.MockTransport(handle)
    ) as transport:
        client.set_async_httpx_client(transport)
        assert_trace(
            await simulate_automation.asyncio(
                slug="billing", client=client, body=SimulateAutomationRequest.from_dict(REQUEST)
            )
        )
