from faas_sdk.preview.models.stored_workflow_publish_policy import StoredWorkflowPublishPolicy
from faas_sdk.preview.models.check_stored_workflow_publication_request import CheckStoredWorkflowPublicationRequest
from faas_sdk.preview.models.check_stored_workflow_publication_response import CheckStoredWorkflowPublicationResponse
from faas_sdk.preview.models.publish_stored_workflow_request import PublishStoredWorkflowRequest


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
    assert CheckStoredWorkflowPublicationRequest.from_dict(request).to_dict() == request
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
    assert CheckStoredWorkflowPublicationResponse.from_dict(response).to_dict() == response
    body = {"expected_version": 7, "check_receipt": response["receipt"]}
    assert PublishStoredWorkflowRequest.from_dict(body).to_dict() == body
    policy = {"mode": "coverage", "version": 2}
    assert StoredWorkflowPublishPolicy.from_dict(policy).to_dict() == policy
