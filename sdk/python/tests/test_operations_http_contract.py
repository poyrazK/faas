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


def test_operation_history_scope_and_projection() -> None:
    from faas_sdk.api.operations import list_platform_tenant_self_operations
    from faas_sdk.models.operation_list_response import OperationListResponse

    app = UUID("11111111-1111-1111-1111-111111111111")
    request = list_platform_tenant_self_operations._get_kwargs(
        app_id=app, scope="staging", name="customer-export", state="succeeded", limit=2, cursor="opaque+/="
    )
    assert request["url"] == "/v1/platform-tenant-self/customer-operations"
    assert request["params"] == {
        "app_id": str(app),
        "scope": "staging",
        "name": "customer-export",
        "state": "succeeded",
        "limit": 2,
        "cursor": "opaque+/=",
    }
    page = {
        "operations": [
            {
                "id": str(app),
                "name": "export",
                "generation": 1,
                "state": "succeeded",
                "completion_delivery": {"state": "failed", "attempts": 2},
                "cancellation_requested": False,
                "latest_sequence": 3,
                "created_at": "2026-10-05T09:00:00Z",
                "updated_at": "2026-10-05T09:00:00Z",
                "expires_at": "2026-10-06T09:00:00Z",
            }
        ],
        "next_cursor": "next",
    }
    parsed = OperationListResponse.from_dict(page)
    assert parsed.operations[0].state == "succeeded"
    assert parsed.operations[0].completion_delivery.state == "failed"
    assert parsed.next_cursor == "next"


def test_operation_account_operator_contract() -> None:
    from faas_sdk.api.operations import (
        get_account_operation_events,
        get_operation_executions,
        list_account_operations,
        retry_operation_delivery,
    )
    from faas_sdk.models.operation_delivery_summary import OperationDeliverySummary
    from faas_sdk.models.operation_executions_response import OperationExecutionsResponse

    tenant = UUID("11111111-1111-1111-1111-111111111111")
    operation = UUID("22222222-2222-2222-2222-222222222222")
    request = list_account_operations._get_kwargs(
        "exports", scope="production", tenant_id=tenant, cursor="opaque+/=", limit=2
    )
    assert request["url"] == "/v1/apps/exports/operations"
    assert request["params"] == {"scope": "production", "tenant_id": str(tenant), "cursor": "opaque+/=", "limit": 2}
    events = get_account_operation_events._get_kwargs("exports", id=operation, after=7)
    assert events["params"] == {"after": 7}
    executions = get_operation_executions._get_kwargs("exports", id=operation, after=1, limit=2)
    assert executions["params"] == {"after": 1, "limit": 2}
    assert retry_operation_delivery._get_kwargs("exports", id=operation)["url"].endswith("/retry-delivery")
    for status in ("awaiting_outcome", "configuration_failed", "pending", "dead"):
        assert OperationDeliverySummary.from_dict({"state": status, "attempts": 0}).to_dict()["state"] == status
    page = {
        "executions": [
            {
                "generation": 2,
                "invocation_id": str(operation),
                "state": "completed",
                "attempts": 1,
                "created_at": "2026-10-05T11:00:00Z",
            }
        ],
        "next_generation": 2,
    }
    parsed = OperationExecutionsResponse.from_dict(page)
    assert parsed.executions[0].generation == 2
    assert parsed.next_generation == 2


def test_definition_discovery_and_submission_identity() -> None:
    from faas_sdk.api.operations import (
        get_operation_definition,
        get_platform_tenant_self_operation_identity,
        list_operation_definitions,
    )
    from faas_sdk.models.operation_definitions_response import OperationDefinitionsResponse
    from faas_sdk.models.operation_tenant_identity import OperationTenantIdentity

    deployment = UUID("11111111-1111-1111-1111-111111111111")
    page = list_operation_definitions._get_kwargs("exports", deployment_id=deployment)
    assert page["url"] == f"/v1/apps/exports/deployments/{deployment}/operation-definitions"
    definition = get_operation_definition._get_kwargs("exports", deployment_id=deployment, name="export")
    assert definition["url"] == page["url"] + "/export"
    assert (
        get_platform_tenant_self_operation_identity._get_kwargs()["url"]
        == "/v1/platform-tenant-self/customer-operations/identity"
    )
    assert OperationDefinitionsResponse.from_dict({"definitions": []}).definitions == []
    parsed = OperationTenantIdentity.from_dict({"account_id": str(deployment), "platform_tenant_id": str(deployment)})
    assert parsed.platform_tenant_id == deployment


def test_operation_doctor_scoped_observation_contract() -> None:
    """ADR-521: delivery and unverified runtime remain separate from submission."""
    from faas_sdk.api.operations import get_operation_doctor
    from faas_sdk.models.operation_doctor_response import OperationDoctorResponse

    deployment = UUID("11111111-1111-4111-8111-111111111111")
    tenant = UUID("22222222-2222-4222-8222-222222222222")
    request = get_operation_doctor._get_kwargs("exports", deployment, tenant_id=tenant, name="export")
    assert request["method"] == "get"
    assert request["url"] == f"/v1/apps/exports/deployments/{deployment}/operation-doctor"
    assert request["params"] == {"tenant_id": str(tenant), "name": "export"}
    assert get_operation_doctor._get_kwargs("exports", deployment, tenant_id=tenant)["params"] == {
        "tenant_id": str(tenant)
    }
    wire = {
        "app_id": str(deployment),
        "scope": "production",
        "deployment_id": str(deployment),
        "platform_tenant_id": str(tenant),
        "plan": "pro",
        "observed_at": "2026-10-05T13:00:00Z",
        "observation_scope": "responding_api_node",
        "submission_state": "eligible",
        "checks": [
            {
                "check": "completion_destination",
                "status": "warning",
                "impact": "delivery",
                "code": "completion_destination_disabled",
                "message": "Disabled.",
            },
            {
                "check": "native_lifecycle",
                "status": "unknown",
                "impact": "qualification",
                "code": "native_lifecycle_unverified",
                "message": "Unverified.",
            },
        ],
    }
    r = OperationDoctorResponse.from_dict(wire)
    assert r.submission_state == "eligible"
    assert r.checks[0].impact == "delivery"
    assert r.checks[1].status == "unknown"
    assert r.to_dict()["checks"] == wire["checks"]


def test_operation_completion_retry_receipt_contract() -> None:
    """ADR-521: explicit replay zero and retry IDs survive generation unchanged."""
    from faas_sdk.api.operations import get_operation_delivery, get_operation_delivery_attempts, retry_operation_delivery_with_receipt
    from faas_sdk.models.operation_delivery_retry_request import OperationDeliveryRetryRequest
    from faas_sdk.models.operation_delivery_retry_response import OperationDeliveryRetryResponse
    from faas_sdk.models.operation_delivery_inspection import OperationDeliveryInspection

    identity = UUID("11111111-1111-1111-1111-111111111111")
    req = OperationDeliveryRetryRequest(retry_id="stable", delivery_id=identity, expected_replay_generation=0)
    wire = retry_operation_delivery_with_receipt._get_kwargs(slug="exports", id=identity, body=req)
    assert wire["url"] == f"/v1/apps/exports/operations/{identity}/delivery-retries"
    assert wire["json"] == {"retry_id": "stable", "delivery_id": str(identity), "expected_replay_generation": 0}
    assert get_operation_delivery._get_kwargs(slug="exports", id=identity)["url"].endswith("/delivery")
    page = get_operation_delivery_attempts._get_kwargs(slug="exports", id=identity, limit=1, cursor="opaque+/=")
    assert page["params"] == {"limit": 1, "cursor": "opaque+/="}
    time = "2026-10-05T12:00:00Z"
    receipt = {"operation_id": str(identity), "retry_id": "stable", "delivery_id": str(identity), "expected_replay_generation": 0, "replay_generation": 1, "state": "queued", "queued_at": time, "expires_at": "2026-10-06T12:00:00Z"}
    parsed = OperationDeliveryRetryResponse.from_dict(receipt)
    assert parsed.state == "queued" and parsed.replay_generation == 1
    report = OperationDeliveryInspection.from_dict({"operation_id": str(identity), "business_state": "succeeded", "operation_expires_at": time, "observed_at": time, "state": "dead", "attempts": 8, "last_response_code": 422, "replay_generation": 0})
    assert report.business_state == "succeeded" and report.state == "dead" and report.replay_generation == 0
