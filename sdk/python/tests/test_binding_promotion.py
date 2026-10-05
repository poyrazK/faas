from uuid import UUID

import httpx

from faas_sdk.api.deployments import promote_deployment_with_bindings
from faas_sdk.client import Client
from faas_sdk.models.binding_promotion_request import BindingPromotionRequest
from faas_sdk.models.problem import Problem


def test_binding_promotion_request_and_structured_blockers() -> None:
    target = "01234567-89ab-cdef-0123-456789abcdef"
    serving = "fedcba98-7654-3210-fedc-ba9876543210"
    kwargs = promote_deployment_with_bindings._get_kwargs(
        target,
        body=BindingPromotionRequest(
            expected_serving_deployment_id=UUID(serving),
            max_verification_age="5m",
            allow_unsupported=True,
        ),
    )
    assert kwargs["method"] == "post"
    assert kwargs["url"] == f"/v1/deployments/{target}/promote"
    assert kwargs["json"] == {
        "expected_serving_deployment_id": serving,
        "max_verification_age": "5m",
        "allow_unsupported": True,
        "require_application_ack": False,
    }
    report = {
        "app": "api",
        "scope": "default",
        "deployment_id": target,
        "expected_deployment_id": target,
        "checked_at": "2026-10-02T12:00:00Z",
        "inventory_generated_at": "2026-10-02T12:00:00Z",
        "max_verification_age": "5m0s",
        "allow_unsupported": True,
        "passed": False,
        "coverage": "partial",
        "bindings": [],
        "runtime": [],
        "issues": [],
        "blockers": [{"code": "verification_expired", "message": "Verify the candidate."}],
        "warnings": [],
    }
    parsed = promote_deployment_with_bindings._parse_response(
        client=Client(base_url="https://example.test"),
        response=httpx.Response(
            409,
            json={
                "title": "Bindings changed",
                "status": 409,
                "code": "bindings_check_changed",
                "bindings_check": report,
            },
        ),
    )
    assert isinstance(parsed, Problem)
    assert parsed.bindings_check.deployment_id == UUID(target)
    assert parsed.bindings_check.blockers[0].code == "verification_expired"
    assert not parsed.bindings_check.passed
    assert parsed.to_dict()["bindings_check"]["blockers"] == report["blockers"]
