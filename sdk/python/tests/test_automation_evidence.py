from faas_sdk.models.automation_revision_response import AutomationRevisionResponse
from faas_sdk.models.publish_automation_request import PublishAutomationRequest

EVIDENCE = {
    "definition_hash": "a" * 64,
    "checked_version": 7,
    "checked_at": "2026-10-09T10:00:00+00:00",
    "scenarios": [{"name": "success", "passed": True, "definition_valid": True, "complete": True}],
    "coverage_required": False,
    "coverage_passed": False,
    "coverage_remaining": 1,
    "exclusions": [{"step": "approval", "code": "wait_timeout_missing", "reason": "reviewed"}],
}


def test_check_evidence_roundtrip():
    body = {"expected_version": 7, "check_evidence": EVIDENCE}
    assert PublishAutomationRequest.from_dict(body).to_dict() == body
    revision = {
        "version": 9,
        "definition": {"name": "invoice", "steps": []},
        "definition_hash": EVIDENCE["definition_hash"],
        "recorded_at": EVIDENCE["checked_at"],
        "legacy_snapshot": False,
        "published_by_account_id": "00000000-0000-0000-0000-000000000001",
        "check_evidence": EVIDENCE,
    }
    assert AutomationRevisionResponse.from_dict(revision).to_dict() == revision
    del revision["check_evidence"]
    assert "check_evidence" not in AutomationRevisionResponse.from_dict(revision).to_dict()
