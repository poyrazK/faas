from uuid import UUID

import httpx

from faas_sdk.api.operations import get_workflow_operation_execution_control
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.operation_workflow_control_response import OperationWorkflowControlResponse


def test_native_control_uses_workload_and_workflow_proof_without_an_invocation_claim():
    operation = UUID("11111111-1111-4111-8111-111111111111")
    run = "22222222-2222-4222-8222-222222222222"
    nonce = "33333333-3333-4333-8333-333333333333"

    def transport(request):
        assert request.method == "GET"
        assert request.url.path == f"/v1/runtime/workflow-operations/{operation}/control"
        assert request.content == b""
        assert request.headers["Authorization"] == "Bearer current-workload"
        assert request.headers["X-Gregale-Operation-Execution-Kind"] == "workflow"
        assert request.headers["X-Gregale-Operation-Workflow-Run-Id"] == run
        assert request.headers["X-Gregale-Operation-Workflow-Step"] == "collect"
        assert request.headers["X-Gregale-Operation-Generation"] == "2"
        assert request.headers["X-Gregale-Operation-Attempt"] == "3"
        assert request.headers["X-Gregale-Operation-Workflow-Capability"] == nonce
        assert "X-Faas-Invocation-Id" not in request.headers
        assert "X-Gregale-Operation-Capability" not in request.headers
        return httpx.Response(
            200,
            json={
                "operation_id": str(operation),
                "workflow_run_id": run,
                "workflow_step": "collect",
                "generation": 2,
                "attempt": 3,
                "cancellation_requested": False,
                "observed_at": "2026-10-06T10:00:00Z",
                "deadline_at": "2026-10-06T10:01:00Z",
                "lease_expires_at": "2026-10-06T10:00:30Z",
                "poll_after_ms": 1000,
            },
        )

    client = AuthenticatedClient(
        base_url="https://api.example.test",
        token="current-workload",
        httpx_args={"transport": httpx.MockTransport(transport)},
    )
    with client:
        control = get_workflow_operation_execution_control.sync(
            operation,
            client=client,
            x_gregale_operation_execution_kind="workflow",
            x_gregale_operation_workflow_run_id=run,
            x_gregale_operation_workflow_step="collect",
            x_gregale_operation_generation=2,
            x_gregale_operation_attempt=3,
            x_gregale_operation_workflow_capability=nonce,
        )
        assert isinstance(control, OperationWorkflowControlResponse)
        assert control.workflow_run_id == UUID(run) and control.operation_id == operation
        assert control.workflow_step == "collect" and control.generation == 2 and control.attempt == 3
        assert control.observed_at < control.lease_expires_at < control.deadline_at
        assert not control.cancellation_requested and control.poll_after_ms == 1000
