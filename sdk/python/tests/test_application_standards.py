"""Guard against silently omitted request models and successful response types."""

import httpx


def test_standard_mutations_preserve_empty_false_and_revocation_over_transport():
    import json
    from uuid import UUID

    from faas_sdk.api.orgs import (
        approve_application_standard_exception,
        revoke_application_standard_exception,
        set_application_standard_local_intent,
    )
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.models.approve_application_standard_exception_request import (
        ApproveApplicationStandardExceptionRequest,
    )
    from faas_sdk.models.revoke_application_standard_exception_request import RevokeApplicationStandardExceptionRequest
    from faas_sdk.models.set_application_standard_local_intent_request import SetApplicationStandardLocalIntentRequest

    resource = "00000000-0000-4000-8000-000000000001"
    stamp = "2026-10-05T12:00:00+00:00"
    base = f"/v1/orgs/acme/application-standard-enrollments/{resource}"
    local = {"expected_revision": 1, "settings": {}, "additional_log_destinations": []}
    approval = {
        "expected_revision": 2,
        "standard_id": resource,
        "version": 1,
        "field": "require_signed",
        "value": False,
        "reason": "Maintenance",
        "expires_at": stamp,
    }
    revoke = {"expected_revision": 3}
    calls = []

    def transport(request):
        assert request.headers["Authorization"] == "Bearer fixture"
        calls.append((request.method, request.url.path, json.loads(request.content)))
        if request.url.path.endswith("/local-intent"):
            return httpx.Response(
                200,
                json={
                    "app_id": resource,
                    "org_id": resource,
                    "local_settings": {},
                    "additional_log_destinations": [],
                    "adoptions": [],
                    "materialized_fields": [],
                    "desired_revision": 2,
                    "persisted_revision": 1,
                    "observed_revision": 0,
                    "state": "pending",
                    "updated_at": stamp,
                },
            )
        revoked = request.url.path.endswith("/revoke")
        return httpx.Response(
            200 if revoked else 201,
            json={
                "id": resource,
                "org_id": resource,
                "app_id": resource,
                "standard_id": resource,
                "version": 1,
                "field": "require_signed",
                "value": False,
                "reason": "Maintenance",
                "expires_at": stamp,
                "approved_by": resource,
                "created_at": stamp,
                "status": "revoked" if revoked else "active",
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="fixture", httpx_args={"transport": httpx.MockTransport(transport)}
    ) as client:
        enrollment = set_application_standard_local_intent.sync(
            "acme",
            UUID(resource),
            client=client,
            body=SetApplicationStandardLocalIntentRequest.from_dict(local),
            idempotency_key="local",
        )
        assert enrollment.desired_revision == 2
        assert enrollment.persisted_revision == 1
        assert enrollment.observed_revision == 0
        approved = approve_application_standard_exception.sync(
            "acme",
            UUID(resource),
            client=client,
            body=ApproveApplicationStandardExceptionRequest.from_dict(approval),
            idempotency_key="approve",
        )
        assert approved.value is False
        assert approved.status == "active"
        revoked = revoke_application_standard_exception.sync(
            "acme",
            UUID(resource),
            UUID(resource),
            client=client,
            body=RevokeApplicationStandardExceptionRequest.from_dict(revoke),
            idempotency_key="revoke",
        )
        assert revoked.status == "revoked"
        assert revoked.reason == approved.reason
    assert calls == [
        ("PUT", base + "/local-intent", local),
        ("POST", base + "/exceptions", approval),
        ("POST", base + f"/exceptions/{resource}/revoke", revoke),
    ]


from faas_sdk.api.orgs import get_application_standard_version, publish_application_standard_version
from faas_sdk.client import Client
from faas_sdk.models.application_standard_version import ApplicationStandardVersion
from faas_sdk.models.create_application_standard_version_request import CreateApplicationStandardVersionRequest


def test_standard_sdk_retains_all_requirement_types_and_parses_success():
    resource = "00000000-0000-4000-8000-000000000001"
    definition = {
        "log_destinations": {"mode": "mandatory", "override": "extend", "value": [resource]},
        "require_signed": {"mode": "mandatory", "value": True},
        "security_policy": {"mode": "mandatory", "override": "narrow", "value": "enforce"},
        "trusted_publishers": {"mode": "restricted", "value": [resource]},
        "egress_cidrs": {"mode": "restricted", "value": ["203.0.113.0/24"]},
        "egress_extra_ports": {"mode": "restricted", "value": [5432]},
    }
    payload = {"expected_version": 0, "definition": definition, "description": "Production"}
    request = CreateApplicationStandardVersionRequest.from_dict(payload)
    kwargs = publish_application_standard_version._get_kwargs("acme", "baseline", body=request)
    assert kwargs["method"] == "post"
    assert kwargs["url"] == "/v1/orgs/acme/application-standards/baseline/versions"
    assert kwargs["json"] == payload

    version = {
        "standard_id": resource,
        "org_id": resource,
        "slug": "baseline",
        "version": 1,
        "definition": definition,
        "definition_hash": "a" * 64,
        "description": "Production",
        "created_by": resource,
        "created_at": "2026-09-30T12:00:00Z",
    }
    client = Client(base_url="https://api.example.test", raise_on_unexpected_status=True)
    for endpoint, status in [(publish_application_standard_version, 201), (get_application_standard_version, 200)]:
        parsed = endpoint._parse_response(client=client, response=httpx.Response(status, json=version))
        assert isinstance(parsed, ApplicationStandardVersion)
        assert parsed.version == 1
        assert parsed.definition.to_dict() == definition


def test_review_sdk_retains_false_and_zero_and_parses_saved_preview():
    from faas_sdk.api.orgs import get_application_standard_review, preview_application_standard_assignment
    from faas_sdk.models.application_standard_review import ApplicationStandardReview
    from faas_sdk.models.application_standard_review_request import ApplicationStandardReviewRequest

    resource = "00000000-0000-4000-8000-000000000001"
    request = {
        "assignment_id": resource,
        "scope": "organization",
        "scope_id": resource,
        "standard_id": resource,
        "admission_version": 2,
        "expected_revision": 1,
        "active": False,
        "batch_size": 10,
    }
    body = ApplicationStandardReviewRequest.from_dict(request)
    kwargs = preview_application_standard_assignment._get_kwargs("acme", body=body, idempotency_key="preview-key")
    assert kwargs["method"] == "post"
    assert kwargs["url"] == "/v1/orgs/acme/application-standard-reviews"
    assert kwargs["json"] == request
    assert kwargs["headers"]["Idempotency-Key"] == "preview-key"
    initial = dict(request, expected_revision=0, active=True)
    del initial["assignment_id"]
    assert ApplicationStandardReviewRequest.from_dict(initial).to_dict() == initial
    review = {
        "id": resource,
        "org_id": resource,
        "created_by": resource,
        "request": request,
        "approval_hash": "a" * 64,
        "applications": [],
        "blockers": [],
        "created_at": "2026-10-04T12:00:00Z",
        "expires_at": "2026-10-04T12:30:00Z",
    }
    client = Client(base_url="https://api.example.test", raise_on_unexpected_status=True)
    for endpoint, status in [(preview_application_standard_assignment, 201), (get_application_standard_review, 200)]:
        parsed = endpoint._parse_response(client=client, response=httpx.Response(status, json=review))
        assert isinstance(parsed, ApplicationStandardReview)
        assert parsed.request.active is False
        assert parsed.request.expected_revision == 1
        assert parsed.expires_at > parsed.created_at


def test_operation_sdk_keeps_persisted_progress_separate_from_observation():
    from uuid import UUID

    from faas_sdk.api.orgs import get_application_standard_operation
    from faas_sdk.models.application_standard_operation import ApplicationStandardOperation

    resource = "00000000-0000-4000-8000-000000000001"
    stamp = "2026-10-04T12:00:00Z"
    app = {
        "app_id": resource,
        "slug": "service",
        "desired_revision": 1,
        "before_settings": {},
        "before_adoptions": [],
        "after_adoptions": [{"assignment_id": resource, "version": 2}],
        "local_settings": {},
        "additional_log_destinations": [],
        "effective": {"values": {}, "sources": {}, "violations": []},
        "changed_fields": [],
    }
    operation = {
        "id": resource,
        "org_id": resource,
        "plan_id": resource,
        "assignment_id": resource,
        "approval_hash": "a" * 64,
        "approved_by": resource,
        "batch_size": 10,
        "state": "waiting",
        "targets": [
            {
                "app_id": resource,
                "position": 0,
                "approved_app": app,
                "state": "persisted",
                "desired_revision": 2,
                "updated_at": stamp,
            }
        ],
        "created_at": stamp,
        "updated_at": stamp,
    }
    kwargs = get_application_standard_operation._get_kwargs("acme", UUID(resource))
    assert kwargs["method"] == "get"
    assert kwargs["url"] == "/v1/orgs/acme/application-standard-operations/" + resource
    client = Client(base_url="https://api.example.test", raise_on_unexpected_status=True)
    parsed = get_application_standard_operation._parse_response(
        client=client, response=httpx.Response(200, json=operation)
    )
    assert isinstance(parsed, ApplicationStandardOperation)
    assert parsed.state == "waiting"
    assert parsed.targets[0].state == "persisted"
    assert parsed.targets[0].desired_revision == 2
    assert parsed.targets[0].approved_app.after_adoptions[0].version == 2


def test_exception_sdk_preserves_history_values_cursors_and_projection_expiry():
    from uuid import UUID

    from faas_sdk.api.orgs import get_application_standard_enrollment, list_application_standard_exceptions
    from faas_sdk.models.application_standard_enrollment import ApplicationStandardEnrollment
    from faas_sdk.models.application_standard_exception_list import ApplicationStandardExceptionList

    resource = "00000000-0000-4000-8000-000000000001"
    stamp = "2026-10-04T12:00:00Z"
    kwargs = list_application_standard_exceptions._get_kwargs("acme", UUID(resource), after=UUID(resource), limit=1)
    assert kwargs["url"] == "/v1/orgs/acme/application-standard-enrollments/" + resource + "/exceptions"
    assert kwargs["params"] == {"after": resource, "limit": 1}
    client = Client(base_url="https://api.example.test", raise_on_unexpected_status=True)
    for value in (True, "enforce", [resource], ["8.8.8.8/32"], [5432], []):
        exception = {
            "id": resource,
            "org_id": resource,
            "app_id": resource,
            "standard_id": resource,
            "version": 1,
            "field": "egress_extra_ports",
            "value": value,
            "reason": "Maintenance",
            "expires_at": stamp,
            "approved_by": resource,
            "created_at": stamp,
            "revoked_by": resource,
            "revoked_at": stamp,
            "status": "revoked",
        }
        payload = {"exceptions": [exception], "next_page_after": resource, "as_of": stamp}
        parsed = list_application_standard_exceptions._parse_response(
            client=client, response=httpx.Response(200, json=payload)
        )
        assert isinstance(parsed, ApplicationStandardExceptionList)
        assert parsed.exceptions[0].value == value
        assert parsed.exceptions[0].reason == "Maintenance"
        assert parsed.exceptions[0].status == "revoked"
        assert str(parsed.next_page_after) == resource
    enrollment = {
        "app_id": resource,
        "org_id": resource,
        "local_settings": {},
        "additional_log_destinations": [],
        "adoptions": [],
        "materialized_fields": [],
        "desired_revision": 2,
        "persisted_revision": 2,
        "observed_revision": 0,
        "state": "persisted",
        "updated_at": stamp,
        "installed_exception_expires_at": stamp,
    }
    parsed_enrollment = get_application_standard_enrollment._parse_response(
        client=client, response=httpx.Response(200, json=enrollment)
    )
    assert isinstance(parsed_enrollment, ApplicationStandardEnrollment)
    assert parsed_enrollment.installed_exception_expires_at is not None
    assert parsed_enrollment.observed_revision == 0


def test_review_approval_and_operator_controls_preserve_microsecond_tokens():
    import json
    from uuid import UUID

    from faas_sdk.api.orgs import (
        abort_application_standard_operation,
        approve_application_standard_review,
        pause_application_standard_operation,
        resume_application_standard_operation,
    )
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.models.approve_application_standard_review_request import ApproveApplicationStandardReviewRequest
    from faas_sdk.models.control_application_standard_operation_request import (
        ControlApplicationStandardOperationRequest,
    )

    resource = "00000000-0000-4000-8000-000000000001"
    stamp = "2026-10-04T12:00:00.123456+00:00"
    digest = "a" * 64
    calls = []

    def transport(request):
        assert request.headers["Authorization"] == "Bearer fixture"
        calls.append((request.method, request.url.path, json.loads(request.content)))
        return httpx.Response(
            201 if request.url.path.endswith("/approve") else 200,
            json={
                "id": resource,
                "org_id": resource,
                "plan_id": resource,
                "assignment_id": resource,
                "approval_hash": digest,
                "approved_by": resource,
                "batch_size": 1,
                "state": "waiting",
                "targets": [],
                "created_at": stamp,
                "updated_at": stamp,
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="fixture", httpx_args={"transport": httpx.MockTransport(transport)}
    ) as client:
        op = approve_application_standard_review.sync(
            "acme",
            UUID(resource),
            client=client,
            body=ApproveApplicationStandardReviewRequest.from_dict({"approval_hash": digest}),
        )
        assert op.updated_at.isoformat() == stamp
        for endpoint in (
            pause_application_standard_operation,
            resume_application_standard_operation,
            abort_application_standard_operation,
        ):
            op = endpoint.sync(
                "acme",
                UUID(resource),
                client=client,
                body=ControlApplicationStandardOperationRequest.from_dict({"expected_updated_at": stamp}),
            )
            assert op.updated_at.isoformat() == stamp
            assert op.state == "waiting"
    base = f"/v1/orgs/acme/application-standard-operations/{resource}"
    assert calls == [
        ("POST", f"/v1/orgs/acme/application-standard-reviews/{resource}/approve", {"approval_hash": digest}),
        *[("POST", f"{base}/{action}", {"expected_updated_at": stamp}) for action in ("pause", "resume", "abort")],
    ]
