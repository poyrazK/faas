import json

import httpx

from faas_sdk.api.workflows import (
    get_automation_failure_policy,
    resume_automation_failure_pause,
    set_automation_failure_policy,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.resume_automation_failure_pause_request import ResumeAutomationFailurePauseRequest
from faas_sdk.models.set_automation_failure_policy_request import SetAutomationFailurePolicyRequest


def test_failure_policy_wire_preserves_false_and_generation():
    body = {
        "expected_version": 2,
        "enabled": False,
        "failure_threshold": 3,
        "min_completed_runs": 5,
        "window_seconds": 300,
    }
    calls = []

    def respond(request: httpx.Request) -> httpx.Response:
        calls.append(request.method)
        assert request.headers["Authorization"] == "Bearer token"
        assert request.url.path.startswith("/v1/apps/billing/automations/invoice/failure-policy")
        if request.method == "PUT":
            assert json.loads(request.content) == body
        if request.method == "POST":
            assert request.url.path.endswith("/resume")
            assert json.loads(request.content) == {"expected_generation": 7}
        return httpx.Response(
            200,
            json={
                "policy": {"version": 3, **{k: v for k, v in body.items() if k != "expected_version"}},
                "paused": request.method != "POST",
                "generation": 8 if request.method == "POST" else 7,
                "observed_failures": 3,
                "observed_completed_runs": 5,
                "pending_runs": 2,
                "running_runs": 1,
                "waiting_runs": 0,
                "retained_events": 4,
                "history": [],
            },
        )

    client = AuthenticatedClient(
        base_url="https://api.example.com", token="token", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    preview = get_automation_failure_policy.sync(slug="billing", name="invoice", client=client)
    assert preview.paused and preview.retained_events == 4
    updated = set_automation_failure_policy.sync(
        slug="billing", name="invoice", client=client, body=SetAutomationFailurePolicyRequest.from_dict(body)
    )
    assert not updated.policy.enabled and updated.paused
    resumed = resume_automation_failure_pause.sync(
        slug="billing", name="invoice", client=client, body=ResumeAutomationFailurePauseRequest(expected_generation=7)
    )
    assert not resumed.paused and resumed.generation == 8
    assert calls == ["GET", "PUT", "POST"]
