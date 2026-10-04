"""ADR-521: generated HTTP contracts preserve customer identity and resume cursors."""

from uuid import UUID

from faas_sdk.api.operations import get_platform_tenant_self_operation_events, start_platform_tenant_self_operation
from faas_sdk.models.operation_accepted_response import OperationAcceptedResponse
from faas_sdk.models.operation_events_response import OperationEventsResponse
from faas_sdk.models.operation_start_request import OperationStartRequest


def test_operation_submission_and_cursor_contract() -> None:
    definition = UUID("11111111-1111-1111-1111-111111111111")
    body = OperationStartRequest(definition_id=definition, input_={"count": 1})
    request = start_platform_tenant_self_operation._get_kwargs(body=body, idempotency_key="stable-export")
    assert request["url"] == "/v1/platform-tenant-self/customer-operations"
    assert request["headers"]["Idempotency-Key"] == "stable-export"
    assert request["json"] == {"definition_id": str(definition), "input": {"count": 1}}
    receipt = {"id": str(definition), "status_url": "/status", "events_url": "/events"}
    assert OperationAcceptedResponse.from_dict(receipt).to_dict() == receipt
    resume = get_platform_tenant_self_operation_events._get_kwargs(id=definition, after=42)
    assert resume["url"] == f"/v1/platform-tenant-self/customer-operations/{definition}/events"
    assert resume["params"] == {"after": 42}
    page = {"events": [], "latest_sequence": 42, "resync_required": False}
    assert OperationEventsResponse.from_dict(page).to_dict() == page
