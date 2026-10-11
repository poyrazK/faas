import datetime
from uuid import UUID

import httpx

from faas_sdk.api.deployments import get_rollback_operation, rollback_app
from faas_sdk.client import Client
from faas_sdk.models import DeploymentResponse, RollbackOperation, RollbackRequest


def test_checked_rollback_exact_request_and_202_receipt():
    target = UUID("a2b9cc53-907f-4b5c-88a4-fd0c21214556")
    current = UUID("5b87c415-7c93-4932-acab-a3c90e98be86")
    request = UUID("3e9f323a-ade6-442b-8444-c91da107fe44")
    operation = RollbackOperation(
        id=request,
        app_id=UUID("142b7504-f03a-4ee2-aeb3-14d922a845d4"),
        scope="default",
        target_deployment_id=target,
        current_deployment_id=current,
        status="preparing",
        service=False,
        created_at=datetime.datetime.now(datetime.UTC),
        updated_at=datetime.datetime.now(datetime.UTC),
    )
    client = Client(base_url="https://example.test")
    kwargs = rollback_app._get_kwargs(
        "api", body=RollbackRequest(target_deployment_id=target, expected_current_deployment_id=current)
    )
    assert kwargs["json"] == {"target_deployment_id": str(target), "expected_current_deployment_id": str(current)}
    parsed = rollback_app._parse_response(
        client=client,
        response=httpx.Response(
            202,
            json={
                "id": str(target),
                "app_id": str(operation.app_id),
                "image_digest": "sha256:" + "a" * 64,
                "kind": "upload",
                "status": "snapshotting",
                "created_at": operation.created_at.isoformat(),
                "rollback_operation": operation.to_dict(),
            },
        ),
    )
    assert isinstance(parsed, DeploymentResponse)
    assert parsed.rollback_operation.id == request
    read = get_rollback_operation._parse_response(client=client, response=httpx.Response(200, json=operation.to_dict()))
    assert isinstance(read, RollbackOperation)
    assert read.target_deployment_id == target
    assert read.current_deployment_id == current
    assert read.status == "preparing"
