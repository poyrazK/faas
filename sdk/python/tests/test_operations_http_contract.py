"""ADR-521: generated HTTP contracts preserve customer identity and resume cursors."""

from io import BytesIO
from uuid import UUID

from faas_sdk.api.operations import get_platform_tenant_self_operation_events, start_platform_tenant_self_operation
from faas_sdk.models.operation_accepted_response import OperationAcceptedResponse
from faas_sdk.models.operation_events_response import OperationEventsResponse
from faas_sdk.models.operation_start_request import OperationStartRequest


def test_direct_job_upload_wire_contract() -> None:
    from faas_sdk.api.operations import reuse_job_operation_upload, upload_job_operation_artifact
    from faas_sdk.models.operation_artifact_upload_request import OperationArtifactUploadRequest
    from faas_sdk.models.operation_job_artifact_response import OperationJobArtifactResponse
    from faas_sdk.types import File

    operation = UUID("11111111-1111-4111-8111-111111111111")
    descriptor = OperationArtifactUploadRequest(
        report_id="csv", name="export.csv", size_bytes=3, sha256="sha256:" + "a" * 64
    )
    native = dict(
        x_gregale_operation_job_run_id=operation,
        x_gregale_operation_job_instance_id=operation,
        x_gregale_operation_generation=1,
        x_gregale_operation_attempt=1,
    )
    upload = upload_job_operation_artifact._get_kwargs(
        operation, body=File(payload=BytesIO(b"csv")), **descriptor.to_dict(), **native
    )
    assert upload["url"] == f"/v1/runtime/job-operations/{operation}/artifact-uploads"
    assert upload["params"] == descriptor.to_dict()
    assert upload["headers"]["Content-Type"] == "application/octet-stream"
    assert upload["headers"]["X-Gregale-Operation-Job-Run-Id"] == str(operation)
    assert upload["headers"]["X-Gregale-Operation-Job-Instance-Id"] == str(operation)
    assert upload["content"].read() == b"csv"
    lookup = reuse_job_operation_upload._get_kwargs(operation, body=descriptor, **native)
    assert lookup["url"].endswith("/artifact-upload-receipts")
    assert lookup["json"] == descriptor.to_dict()
    reference = f"operation://{operation}/artifacts/{operation}"
    receipt = OperationJobArtifactResponse.from_dict(
        {
            "available": True,
            "artifact": {
                "id": str(operation),
                "name": "export.csv",
                "uri": reference,
                "size_bytes": 3,
                "sha256": descriptor.sha256,
            },
        }
    )
    assert receipt.artifact.uri == reference

    # Exercise HTTPX's real header/content encoding with the generated UUID
    # signature and a tokenless client carrying only the native capability.
    import httpx

    from faas_sdk.client import Client

    paths = []

    def receive(request):
        paths.append(request.url.path)
        assert "Authorization" not in request.headers
        assert request.headers["X-Gregale-Operation-Job-Capability"] == "b" * 64
        assert request.headers["X-Gregale-Operation-Job-Run-Id"] == str(operation)
        if request.url.path.endswith("/artifact-uploads"):
            assert request.read() == b"csv"
            assert request.url.params["sha256"] == descriptor.sha256
        return httpx.Response(200, json=receipt.to_dict())

    with Client(
        base_url="https://api.example.test",
        headers={"X-Gregale-Operation-Job-Capability": "b" * 64},
        httpx_args={"transport": httpx.MockTransport(receive)},
    ) as client:
        assert upload_job_operation_artifact.sync_detailed(
            operation, client=client, body=File(payload=BytesIO(b"csv")), **descriptor.to_dict(), **native
        ).parsed.available
        assert reuse_job_operation_upload.sync_detailed(
            operation, client=client, body=descriptor, **native
        ).parsed.available
    assert len(paths) == 2


def test_direct_workflow_upload_with_current_native_proof() -> None:
    import json

    import httpx

    from faas_sdk.api.operations import reuse_workflow_operation_upload, upload_workflow_operation_artifact
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.models.operation_artifact_upload_request import OperationArtifactUploadRequest
    from faas_sdk.models.operation_workflow_artifact_response import OperationWorkflowArtifactResponse
    from faas_sdk.models.reuse_workflow_operation_upload_x_gregale_operation_execution_kind import (
        ReuseWorkflowOperationUploadXGregaleOperationExecutionKind,
    )
    from faas_sdk.models.upload_workflow_operation_artifact_x_gregale_operation_execution_kind import (
        UploadWorkflowOperationArtifactXGregaleOperationExecutionKind,
    )
    from faas_sdk.types import File

    operation = UUID("11111111-1111-4111-8111-111111111111")
    run_id = "22222222-2222-4222-8222-222222222222"
    capability = "33333333-3333-4333-8333-333333333333"
    descriptor = OperationArtifactUploadRequest(
        report_id="csv", name="export.csv", size_bytes=3, sha256="sha256:" + "a" * 64
    )
    proof = dict(
        x_gregale_operation_attempt=2,
        x_gregale_operation_workflow_run_id=run_id,
        x_gregale_operation_workflow_step="finish",
        x_gregale_operation_generation=2,
        x_gregale_operation_workflow_capability=capability,
    )
    upload_kind: UploadWorkflowOperationArtifactXGregaleOperationExecutionKind = "workflow"
    reuse_kind: ReuseWorkflowOperationUploadXGregaleOperationExecutionKind = "workflow"
    receipt = OperationWorkflowArtifactResponse.from_dict(
        {
            "available": True,
            "artifact": {
                "id": str(operation),
                "uri": f"operation://{operation}/artifacts/{operation}",
                "name": descriptor.name,
                "size_bytes": 3,
                "sha256": descriptor.sha256,
            },
        }
    )
    paths = []

    def receive(request):
        paths.append(request.url.path)
        assert request.headers["Authorization"] == "Bearer workload"
        assert request.headers["X-Gregale-Operation-Execution-Kind"] == "workflow"
        assert request.headers["X-Gregale-Operation-Workflow-Run-Id"] == run_id
        assert request.headers["X-Gregale-Operation-Workflow-Step"] == "finish"
        assert request.headers["X-Gregale-Operation-Workflow-Capability"] == capability
        assert request.headers["X-Gregale-Operation-Generation"] == "2"
        assert request.headers["X-Gregale-Operation-Attempt"] == "2"
        assert "X-Faas-Invocation-Id" not in request.headers
        if request.url.path.endswith("/artifact-uploads"):
            assert request.read() == b"csv"
            assert dict(request.url.params) == {**descriptor.to_dict(), "size_bytes": "3"}
            assert request.headers["Content-Type"] == "application/octet-stream"
        else:
            assert request.url.path.endswith("/artifact-upload-receipts")
            assert json.loads(request.read()) == descriptor.to_dict()
        return httpx.Response(200, json=receipt.to_dict())

    with AuthenticatedClient(
        base_url="https://api.example.test", token="workload", httpx_args={"transport": httpx.MockTransport(receive)}
    ) as client:
        assert upload_workflow_operation_artifact.sync_detailed(
            operation,
            client=client,
            body=File(payload=BytesIO(b"csv")),
            x_gregale_operation_execution_kind=upload_kind,
            **descriptor.to_dict(),
            **proof,
        ).parsed.available
        assert reuse_workflow_operation_upload.sync_detailed(
            operation,
            client=client,
            body=descriptor,
            x_gregale_operation_execution_kind=reuse_kind,
            **proof,
        ).parsed.available
    assert paths == [
        f"/v1/runtime/workflow-operations/{operation}/artifact-uploads",
        f"/v1/runtime/workflow-operations/{operation}/artifact-upload-receipts",
    ]


def test_recovery_inspection_preview_and_revision_fence() -> None:
    from faas_sdk.api.operations import inspect_operation_recovery, preview_operation_recovery, recover_operation
    from faas_sdk.models.operation_recovery_preview import OperationRecoveryPreview
    from faas_sdk.models.operation_recovery_preview_request import OperationRecoveryPreviewRequest
    from faas_sdk.models.operation_recovery_request import OperationRecoveryRequest

    operation = UUID("11111111-1111-1111-1111-111111111111")
    revision = "sha256:" + "a" * 64
    assert inspect_operation_recovery._get_kwargs("exports", id=operation)["url"].endswith("/recovery-inspection")
    request = preview_operation_recovery._get_kwargs(
        "exports", id=operation, body=OperationRecoveryPreviewRequest(expected_generation=1, resolution="safe_to_retry")
    )
    assert request["method"] == "post"
    assert request["url"].endswith("/recovery-preview")
    assert request["json"] == {"expected_generation": 1, "resolution": "safe_to_retry"}
    inspection = {
        "operation_id": str(operation),
        "generation": 1,
        "state": "requires_reconciliation",
        "cancellation_requested": False,
        "execution_kind": "workflow",
        "execution_state": "dead",
        "attempt": 1,
        "deployment_id": str(operation),
        "steps": [{"name": "finish", "state": "dead", "attempt": 1, "confirmed": False, "outcome_unknown": True}],
        "artifacts": [],
        "retry_blockers": [],
        "inspection_revision": revision,
        "observed_at": "2026-10-06T12:00:00Z",
    }
    wire = {
        "inspection": inspection,
        "resolution": "safe_to_retry",
        "eligible": False,
        "evidence_required": True,
        "blockers": ["workflow_concurrency_limit"],
        "reused_steps": [],
        "reopened_steps": ["finish"],
        "reusable_artifact_ids": [],
        "publish_artifact_ids": [],
        "starts_new_execution": False,
        "clears_artifact_references": False,
    }
    parsed = OperationRecoveryPreview.from_dict(wire)
    assert not parsed.eligible and parsed.evidence_required
    assert parsed.inspection.steps[0].outcome_unknown
    assert parsed.inspection.inspection_revision == revision
    apply = recover_operation._get_kwargs(
        "exports",
        id=operation,
        body=OperationRecoveryRequest(
            recovery_id="decision",
            expected_generation=1,
            resolution="safe_to_retry",
            evidence="provider ledger checked",
            expected_inspection_revision=revision,
        ),
    )
    assert apply["json"]["expected_inspection_revision"] == revision


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
            {
                "check": "execution_preview",
                "status": "observed",
                "impact": "submission",
                "code": "preview_cohort_observed",
                "message": "Job allowed.",
                "name": "export",
                "execution_kind": "job",
            },
        ],
    }
    r = OperationDoctorResponse.from_dict(wire)
    assert r.submission_state == "eligible"
    assert r.checks[0].impact == "delivery"
    assert r.checks[1].status == "unknown"
    assert r.checks[2].execution_kind == "job"
    assert r.to_dict()["checks"] == wire["checks"]


def test_operation_completion_retry_receipt_contract() -> None:
    """ADR-521: explicit replay zero and retry IDs survive generation unchanged."""
    from faas_sdk.api.operations import (
        get_operation_delivery,
        get_operation_delivery_attempts,
        retry_operation_delivery_with_receipt,
    )
    from faas_sdk.models.operation_delivery_inspection import OperationDeliveryInspection
    from faas_sdk.models.operation_delivery_retry_request import OperationDeliveryRetryRequest
    from faas_sdk.models.operation_delivery_retry_response import OperationDeliveryRetryResponse

    identity = UUID("11111111-1111-1111-1111-111111111111")
    req = OperationDeliveryRetryRequest(retry_id="stable", delivery_id=identity, expected_replay_generation=0)
    wire = retry_operation_delivery_with_receipt._get_kwargs(slug="exports", id=identity, body=req)
    assert wire["url"] == f"/v1/apps/exports/operations/{identity}/delivery-retries"
    assert wire["json"] == {"retry_id": "stable", "delivery_id": str(identity), "expected_replay_generation": 0}
    assert get_operation_delivery._get_kwargs(slug="exports", id=identity)["url"].endswith("/delivery")
    page = get_operation_delivery_attempts._get_kwargs(slug="exports", id=identity, limit=1, cursor="opaque+/=")
    assert page["params"] == {"limit": 1, "cursor": "opaque+/="}
    time = "2026-10-05T12:00:00Z"
    receipt = {
        "operation_id": str(identity),
        "retry_id": "stable",
        "delivery_id": str(identity),
        "expected_replay_generation": 0,
        "replay_generation": 1,
        "state": "queued",
        "queued_at": time,
        "expires_at": "2026-10-06T12:00:00Z",
    }
    parsed = OperationDeliveryRetryResponse.from_dict(receipt)
    assert parsed.state == "queued" and parsed.replay_generation == 1
    report = OperationDeliveryInspection.from_dict(
        {
            "operation_id": str(identity),
            "business_state": "succeeded",
            "operation_expires_at": time,
            "observed_at": time,
            "state": "dead",
            "attempts": 8,
            "last_response_code": 422,
            "replay_generation": 0,
        }
    )
    assert report.business_state == "succeeded" and report.state == "dead" and report.replay_generation == 0


def test_workflow_operation_definition_and_execution_identity() -> None:
    from faas_sdk.models.operation_definition_spec import OperationDefinitionSpec
    from faas_sdk.models.operation_execution import OperationExecution
    from faas_sdk.types import UNSET

    definition = {
        "name": "export",
        "workflow": "export-chain",
        "method": "POST",
        "path": "/exports",
        "owner": "platform_tenant",
        "input_schema": True,
        "output_schema": True,
        "progress_stages": ["collect", "finish"],
        "recovery": "reconcile_on_unknown",
    }
    assert OperationDefinitionSpec.from_dict(definition).to_dict() == definition
    run_id = "11111111-1111-1111-1111-111111111111"
    execution = OperationExecution.from_dict(
        {
            "generation": 2,
            "workflow_run_id": run_id,
            "state": "succeeded",
            "attempts": 3,
            "created_at": "2026-10-06T00:00:00+00:00",
        }
    )
    assert execution.workflow_run_id == UUID(run_id)
    assert execution.invocation_id is UNSET
    assert "invocation_id" not in execution.to_dict()
    assert execution.to_dict()["workflow_run_id"] == run_id


def test_submission_lookup_and_principal_feature_fences() -> None:
    from faas_sdk.api.operations import lookup_platform_tenant_self_operation_submission
    from faas_sdk.models.operation_submission_lookup_request import OperationSubmissionLookupRequest
    from faas_sdk.models.operation_submission_lookup_response import OperationSubmissionLookupResponse
    from faas_sdk.models.operation_submission_scope import OperationSubmissionScope
    from faas_sdk.models.operation_tenant_identity import OperationTenantIdentity

    operation = UUID("11111111-1111-4111-8111-111111111111")
    identity = OperationTenantIdentity(account_id=operation, platform_tenant_id=operation)
    scope = OperationSubmissionScope(app_id=operation, scope="default", name="export")
    request = lookup_platform_tenant_self_operation_submission._get_kwargs(
        body=OperationSubmissionLookupRequest(
            app_id=operation, scope="default", name="export", idempotency_key="saved-key", expected_identity=identity
        )
    )
    assert request["method"] == "post"
    assert request["url"] == "/v1/platform-tenant-self/customer-operations/submissions/lookup"
    assert "params" not in request
    assert request["json"] == {
        **scope.to_dict(),
        "idempotency_key": "saved-key",
        "expected_identity": identity.to_dict(),
    }
    fenced = start_platform_tenant_self_operation._get_kwargs(
        body=OperationStartRequest(
            definition_id=operation, input_={"count": 1}, expected_identity=identity, expected_scope=scope
        ),
        idempotency_key="saved-key",
    )
    assert fenced["json"]["expected_identity"] == identity.to_dict()
    assert fenced["json"]["expected_scope"] == scope.to_dict()
    found = OperationSubmissionLookupResponse.from_dict(
        {
            "state": "accepted",
            "accepted_at": "2026-10-06T12:00:00Z",
            "receipt": {"id": str(operation), "status_url": "/status", "events_url": "/events"},
        }
    )
    assert found.receipt.id == operation
    assert found.accepted_at.isoformat() == "2026-10-06T12:00:00+00:00"
    assert OperationSubmissionLookupResponse.from_dict({"state": "unresolved"}).state == "unresolved"


def test_direct_http_upload_with_workload_jwt_and_invocation_proof() -> None:
    import httpx

    from faas_sdk.api.operations import reuse_operation_upload, upload_operation_artifact
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.models.operation_artifact_upload_request import OperationArtifactUploadRequest
    from faas_sdk.models.operation_artifact_upload_response import OperationArtifactUploadResponse
    from faas_sdk.types import File

    operation = UUID("11111111-1111-4111-8111-111111111111")
    descriptor = OperationArtifactUploadRequest(
        report_id="csv", name="export.csv", size_bytes=3, sha256="sha256:" + "a" * 64
    )
    proof = dict(x_faas_invocation_id=operation, x_gregale_operation_attempt=1, x_gregale_operation_capability="b" * 64)
    receipt = OperationArtifactUploadResponse.from_dict(
        {
            "available": True,
            "artifact": {
                "id": str(operation),
                "uri": f"operation://{operation}/artifacts/{operation}",
                "name": descriptor.name,
                "size_bytes": 3,
                "sha256": descriptor.sha256,
            },
        }
    )
    paths = []

    def receive(request):
        paths.append(request.url.path)
        assert request.headers["Authorization"] == "Bearer workload"
        assert request.headers["X-Faas-Invocation-Id"] == str(operation)
        assert request.headers["X-Gregale-Operation-Capability"] == "b" * 64
        assert request.headers["X-Gregale-Operation-Attempt"] == "1"
        if request.url.path.endswith("/artifact-uploads"):
            assert request.read() == b"csv"
            assert request.url.params["sha256"] == descriptor.sha256
            assert request.headers["Content-Type"] == "application/octet-stream"
        return httpx.Response(200, json=receipt.to_dict())

    with AuthenticatedClient(
        base_url="https://api.example.test", token="workload", httpx_args={"transport": httpx.MockTransport(receive)}
    ) as client:
        assert upload_operation_artifact.sync_detailed(
            operation, client=client, body=File(payload=BytesIO(b"csv")), **descriptor.to_dict(), **proof
        ).parsed.available
        assert reuse_operation_upload.sync_detailed(operation, client=client, body=descriptor, **proof).parsed.available
    assert len(paths) == 2
