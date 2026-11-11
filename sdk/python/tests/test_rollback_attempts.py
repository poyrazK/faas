"""adr: 20261105000513 — stable rollback identity never leaks into receipt reads."""
import pytest
from uuid import UUID

from faas_sdk.api.deployments import rollback_app, get_rollback_operation
from faas_sdk.models.rollback_request import RollbackRequest
from faas_sdk.models.rollback_operation import RollbackOperation


@pytest.mark.parametrize("service", [False, True])
def test_rollback_attempt_header_is_explicit_and_scoped(service: bool) -> None:
    attempt = "3e9f323aade6442b8444c91da107fe44"
    target = UUID("a2b9cc53-907f-4b5c-88a4-fd0c21214556")
    current = UUID("5b87c415-7c93-4932-acab-a3c90e98be86")
    body = RollbackRequest(target_deployment_id=target, expected_current_deployment_id=current)
    kwargs = rollback_app._get_kwargs("alpha", body=body, rollback_attempt_id=attempt)
    assert kwargs["headers"]["Rollback-Attempt-ID"] == attempt
    assert kwargs["json"]["target_deployment_id"] == str(target)
    assert "Rollback-Attempt-ID" not in rollback_app._get_kwargs("alpha", body=body)["headers"]
    assert get_rollback_operation._get_kwargs("alpha", UUID(attempt)) == {
        "method": "get", "url": f"/v1/apps/alpha/rollbacks/{UUID(attempt)}"
    }

    wire = {"id": str(UUID(attempt)), "app_id": "142b7504-f03a-4ee2-aeb3-14d922a845d4", "scope": "default", "target_deployment_id": str(target), "current_deployment_id": str(current), "status": "preparing", "service": service, "created_at": "2026-11-05T08:24:46Z", "updated_at": "2026-11-05T08:24:46Z"}
    operation = RollbackOperation.from_dict(wire)
    assert operation.service is service and operation.to_dict()["service"] is service
