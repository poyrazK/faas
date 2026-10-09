import httpx

from faas_sdk import AuthenticatedClient
from faas_sdk.api.workflows import (
    get_platform_tenant_self_workflow_run_diagnostics,
    get_workflow_run_diagnostics,
)
from faas_sdk.models.workflow_run_diagnostics_response import WorkflowRunDiagnosticsResponse


def test_workflow_diagnostics_account_and_tenant_read_routes():
    paths = []
    payload = {
        "run_id": "00000000-0000-4000-8000-000000000001",
        "workflow_name": "invoice",
        "status": "dead",
        "observed_at": "2026-10-07T16:00:00Z",
        "deployment_id": "00000000-0000-4000-8000-000000000002",
        "legacy_unpinned": False,
        "state_reason": "dead",
        "due_age_seconds": 0,
        "stale_lease": False,
        "steps": [{"step_name": "charge", "kind": "action", "status": "dead", "attempt": 1, "retry_base": 0}],
        "resume": {
            "eligible": False,
            "expected_resume_count": 0,
            "reopened_steps": [],
            "preserved_steps": ["charge"],
            "blockers": [
                {"code": "unsafe_mutation", "message": "This mutation cannot be repeated.", "step_name": "charge"}
            ],
        },
    }

    def handle(request):
        assert request.method == "GET"
        assert request.content == b""
        assert "idempotency-key" not in request.headers
        paths.append(request.url.path)
        return httpx.Response(200, json=payload, headers={"cache-control": "no-store"})

    client = AuthenticatedClient(base_url="https://api.example.com", token="read-token")
    with httpx.Client(base_url="https://api.example.com", transport=httpx.MockTransport(handle)) as transport:
        client.set_httpx_client(transport)
        for operation in [get_workflow_run_diagnostics, get_platform_tenant_self_workflow_run_diagnostics]:
            result = operation.sync(id=payload["run_id"], client=client)
            assert isinstance(result, WorkflowRunDiagnosticsResponse)
            assert result.resume.expected_resume_count == 0
            assert result.resume.blockers[0].code == "unsafe_mutation"
            assert result.resume.blockers[0].message == "This mutation cannot be repeated."
            assert str(result.deployment_id) == payload["deployment_id"]
            assert result.to_dict()["resume"] == payload["resume"]
    assert paths == [
        f"/v1/workflows/runs/{payload['run_id']}/diagnostics",
        f"/v1/platform-tenant-self/workflows/runs/{payload['run_id']}/diagnostics",
    ]
