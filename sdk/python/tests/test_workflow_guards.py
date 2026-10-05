from datetime import datetime

from faas_sdk.models.workflow_guard_spec import WorkflowGuardSpec
from faas_sdk.models.workflow_spec import WorkflowSpec
from faas_sdk.models.workflow_step_response import WorkflowStepResponse


def test_nested_guard_roundtrip_preserves_json_types_and_large_integer():
    guard = {
        "all": [
            {"ref": "input.amount", "op": "gt", "value": 9007199254740993},
            {
                "any": [
                    {"ref": "input.active", "op": "eq", "value": True},
                    {"not": {"ref": "input.deleted", "op": "eq", "value": None}},
                ]
            },
        ]
    }
    assert WorkflowGuardSpec.from_dict(guard).to_dict() == guard
    definition = {"name": "invoice", "steps": [{"name": "send", "run": "send", "when": guard}]}
    assert WorkflowSpec.from_dict(definition).to_dict() == definition


def test_false_guard_inspection_preserves_false_and_reason():
    data = {
        "step_name": "send",
        "status": "skipped",
        "attempt": 0,
        "created_at": "2026-10-03T12:00:00Z",
        "when_matched": False,
        "when_evaluated_at": "2026-10-03T12:01:00Z",
        "skip_reason": "when_false",
    }
    result = WorkflowStepResponse.from_dict(data).to_dict()
    assert result["when_matched"] is False
    assert result["skip_reason"] == "when_false"
    assert datetime.fromisoformat(result["when_evaluated_at"]) == datetime.fromisoformat(data["when_evaluated_at"])
