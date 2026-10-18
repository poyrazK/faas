from faas_sdk.models.mcp_readiness_policy_diff import MCPReadinessPolicyDiff
from faas_sdk.models.mcp_readiness_policy_history import MCPReadinessPolicyHistory
from faas_sdk.models.restore_mcp_readiness_policy_request import RestoreMCPReadinessPolicyRequest


def test_policy_history_restore_and_diff_round_trip():
    revision = "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb"
    current = "cb136aee-df09-4663-9f09-810fd6e4d6a2"
    request = {"scope": "staging", "expected_revision": current, "reason": "reviewed restore"}
    assert RestoreMCPReadinessPolicyRequest.from_dict(request).to_dict() == request
    payload = {
        "policies": [
            {
                "scope": "staging",
                "revision": current,
                "restored_from": revision,
                "enabled": False,
                "endpoint": "/mcp",
                "max_age_seconds": 300,
                "roles": [],
                "updated_at": "2026-10-16T12:00:00Z",
                "reason": "reviewed restore",
                "updated_by": "reviewer",
            }
        ],
        "next_before_revision": current,
    }
    history = MCPReadinessPolicyHistory.from_dict(payload)
    assert history.policies[0].restored_from == revision
    assert history.to_dict()["next_before_revision"] == current
    diff = {
        "scope": "staging",
        "from_revision": revision,
        "to_revision": current,
        "changes": [
            {"path": "/endpoint", "before": "/mcp", "after": "/staging-mcp"},
            {"path": "/roles/reader", "before": None, "after": {"name": "reader"}},
        ],
    }
    assert MCPReadinessPolicyDiff.from_dict(diff).to_dict() == diff
