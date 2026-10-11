from faas_sdk.models.automation_publish_policy import AutomationPublishPolicy
from faas_sdk.models.check_automation_publication_request import CheckAutomationPublicationRequest
from faas_sdk.models.check_automation_publication_response import CheckAutomationPublicationResponse
from faas_sdk.models.publish_automation_request import PublishAutomationRequest


def test_server_checks_wire_roundtrip():
    request = {
        "expected_version": 7,
        "require_coverage": True,
        "scenarios": [
            {
                "name": "success",
                "simulation": {"definition": {"name": "checked", "steps": []}},
                "expectations": [{"step": "send", "output": None, "attempt_count": 0, "attempts": []}],
            }
        ],
        "exclusions": [],
    }
    assert CheckAutomationPublicationRequest.from_dict(request).to_dict() == request
    response = {
        "receipt": "b" * 64,
        "expires_at": "2026-10-09T10:30:00+00:00",
        "evidence": {
            "server_verified": True,
            "definition_hash": "a" * 64,
            "checked_version": 7,
            "checked_at": "2026-10-09T10:00:00+00:00",
            "scenarios": [{"name": "success", "passed": True, "definition_valid": True, "complete": True}],
            "coverage_required": True,
            "coverage_passed": True,
            "coverage_remaining": 0,
            "exclusions": [],
        },
    }
    assert CheckAutomationPublicationResponse.from_dict(response).to_dict() == response
    body = {"expected_version": 7, "check_receipt": response["receipt"]}
    assert PublishAutomationRequest.from_dict(body).to_dict() == body
    policy = {"mode": "coverage", "version": 2}
    assert AutomationPublishPolicy.from_dict(policy).to_dict() == policy
