"""ADR-639: native proofs and explicit verified-copy availability."""

from typing import get_args
from uuid import UUID

import httpx

from faas_sdk.api.operations import prepare_workflow_operation_artifact, reuse_workflow_operation_artifact
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.operation_artifact_request import OperationArtifactRequest
from faas_sdk.models.operation_event_type import OperationEventType
from faas_sdk.models.operation_workflow_artifact_response import OperationWorkflowArtifactResponse


def test_workflow_copy_receipt_uses_native_proof_and_string_headers():
    operation = UUID("11111111-1111-4111-8111-111111111111")
    run = "22222222-2222-4222-8222-222222222222"
    nonce = "33333333-3333-4333-8333-333333333333"
    declaration = {
        "report_id": "csv",
        "name": "export.csv",
        "uri": f"obj://{operation}/{run}/export.csv",
        "size_bytes": 3,
        "sha256": "sha256:" + "a" * 64,
    }
    requests = []

    def transport(request):
        requests.append(request)
        assert request.headers["X-Gregale-Operation-Execution-Kind"] == "workflow"
        assert request.headers["X-Gregale-Operation-Workflow-Run-Id"] == run
        assert request.headers["X-Gregale-Operation-Workflow-Capability"] == nonce
        assert request.headers["X-Gregale-Operation-Generation"] == "2"
        assert request.headers["X-Gregale-Operation-Attempt"] == "3"
        assert "X-Faas-Invocation-Id" not in request.headers
        assert "X-Gregale-Operation-Capability" not in request.headers
        assert request.headers["Authorization"] == "Bearer current-workload"
        if len(requests) == 1:
            assert request.url.path.endswith("/artifact-receipts")
            return httpx.Response(200, json={"available": False})
        assert request.url.path.endswith("/artifacts")
        artifact = {key: value for key, value in declaration.items() if key != "report_id"}
        artifact["id"] = str(operation)
        return httpx.Response(200, json={"available": True, "artifact": artifact})

    client = AuthenticatedClient(
        base_url="https://api.example.test",
        token="current-workload",
        httpx_args={"transport": httpx.MockTransport(transport)},
    )
    with client:
        proof = dict(
            body=OperationArtifactRequest.from_dict(declaration),
            x_gregale_operation_attempt=3,
            x_gregale_operation_execution_kind="workflow",
            x_gregale_operation_workflow_run_id=run,
            x_gregale_operation_workflow_step="finish",
            x_gregale_operation_generation=2,
            x_gregale_operation_workflow_capability=nonce,
        )
        absent = reuse_workflow_operation_artifact.sync(operation, client=client, **proof)
        assert isinstance(absent, OperationWorkflowArtifactResponse) and absent.available is False
        prepared = prepare_workflow_operation_artifact.sync(operation, client=client, **proof)
        assert isinstance(prepared, OperationWorkflowArtifactResponse) and prepared.available is True
        assert prepared.artifact.id == operation
    assert "artifact_prepared" in get_args(OperationEventType)
