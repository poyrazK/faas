import json

import httpx

from faas_sdk import AuthenticatedClient
from faas_sdk.api.workflows import list_workflow_resumes, resume_workflow_run
from faas_sdk.models.list_workflow_resumes_response import ListWorkflowResumesResponse
from faas_sdk.models.resume_workflow_run_request import ResumeWorkflowRunRequest
from faas_sdk.models.workflow_run_response import WorkflowRunResponse
from faas_sdk.models.workflow_step_response import WorkflowStepResponse


def test_resume_routes_and_zero_revision():
    requests = []

    def handle(request):
        requests.append(request)
        if request.method == "POST":
            assert request.url.path == "/v1/workflows/runs/run/resume"
            assert json.loads(request.content) == {"expected_resume_count": 0}
            return httpx.Response(
                200,
                json={
                    "id": "00000000-0000-0000-0000-000000000001",
                    "app_id": "00000000-0000-0000-0000-000000000002",
                    "workflow_name": "batch",
                    "status": "pending",
                    "resume_count": 1,
                    "scheduled_for": "2026-10-03T12:00:00Z",
                    "created_at": "2026-10-03T12:00:00Z",
                    "updated_at": "2026-10-03T12:00:00Z",
                },
            )
        assert request.url.path == "/v1/workflows/runs/run/resumes"
        return httpx.Response(
            200,
            json={
                "resumes": [
                    {
                        "run_id": "00000000-0000-0000-0000-000000000001",
                        "resume_number": 1,
                        "account_id": "00000000-0000-0000-0000-000000000003",
                        "previous_status": "dead",
                        "resumed_steps": ["send"],
                        "created_at": "2026-10-03T12:00:00Z",
                    }
                ]
            },
        )

    client = AuthenticatedClient(base_url="https://api.example.com", token="token")
    with httpx.Client(base_url="https://api.example.com", transport=httpx.MockTransport(handle)) as transport:
        client.set_httpx_client(transport)
        run = resume_workflow_run.sync(id="run", client=client, body=ResumeWorkflowRunRequest(expected_resume_count=0))
        assert isinstance(run, WorkflowRunResponse)
        assert run.resume_count == 1
        history = list_workflow_resumes.sync(id="run", client=client)
        assert isinstance(history, ListWorkflowResumesResponse)
        assert history.resumes[0].resumed_steps == ["send"]
    assert len(requests) == 2


def test_retry_budget_and_empty_resume_history_round_trip():
    step = {
        "step_name": "send",
        "status": "running",
        "attempt": 4,
        "retry_base": 3,
        "created_at": "2026-10-03T12:00:00Z",
    }
    value = WorkflowStepResponse.from_dict(step).to_dict()
    assert value["attempt"] == 4
    assert value["retry_base"] == 3
    assert ListWorkflowResumesResponse.from_dict({"resumes": []}).to_dict() == {"resumes": []}
    assert ResumeWorkflowRunRequest.from_dict({"expected_resume_count": 0}).to_dict() == {"expected_resume_count": 0}
