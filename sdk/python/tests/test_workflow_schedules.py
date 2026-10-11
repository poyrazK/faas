import httpx
import pytest

from faas_sdk.api.workflows import list_workflow_schedules
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.workflow_trigger_spec import WorkflowTriggerSpec


@pytest.mark.parametrize("input_value", [None, False, 7, "daily", [1, 2], {"report": "daily"}])
def test_scheduled_trigger_preserves_arbitrary_json_input(input_value):
    wire = {"type": "schedule", "schedule": "0 7 * * *", "input": input_value, "enabled": False}
    assert WorkflowTriggerSpec.from_dict(wire).to_dict() == wire


def test_schedule_inspection_preserves_runtime_blocker():
    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/reports/workflows/schedules"
        assert request.headers["Authorization"] == "Bearer test-token"
        return httpx.Response(
            200,
            json={"runtime_enabled": False, "unavailable_reason": "runtime_disabled", "schedules": []},
        )

    with AuthenticatedClient(
        base_url="https://example.test", token="test-token", httpx_args={"transport": httpx.MockTransport(respond)}
    ) as client:
        response = list_workflow_schedules.sync("reports", client=client)
    assert response is not None
    assert response.to_dict() == {
        "runtime_enabled": False,
        "unavailable_reason": "runtime_disabled",
        "schedules": [],
    }


def test_catch_up_trigger_preserves_policy_window_and_input():
    wire = {
        "type": "schedule",
        "schedule": "0 7 * * *",
        "catch_up": "latest",
        "catch_up_window": "2h",
        "input": {"report": "daily"},
    }
    assert WorkflowTriggerSpec.from_dict(wire).to_dict() == wire
    with pytest.raises(TypeError):
        WorkflowTriggerSpec.from_dict({**wire, "catch_up": "all"})
