from faas_sdk.models.workflow_for_each_spec import WorkflowForEachSpec
from faas_sdk.models.workflow_spec import WorkflowSpec
from faas_sdk.models.workflow_step_response import WorkflowStepResponse


def test_iteration_definition_preserves_nested_mapping_and_retry():
    loop = {
        "items": "input.items",
        "action": {
            "outbound": {
                "integration_id": "00000000-0000-0000-0000-000000000001",
                "method": "POST",
                "path": "/send",
                "idempotency_supported": True,
            },
            "input": {"item": "{{input.item}}", "index": "{{input.index}}", "n": 9007199254740993},
            "timeout": "30s",
            "retry": {"max_attempts": 3, "backoff": "exponential"},
        },
    }
    assert WorkflowForEachSpec.from_dict(loop).to_dict() == loop
    definition = {"name": "batch", "steps": [{"name": "send", "for_each": loop}]}
    assert WorkflowSpec.from_dict(definition).to_dict() == definition


def test_iteration_inspection_preserves_zero_count_index_and_ordered_results():
    output = [{"n": 9007199254740993}, [True, None], "{{input.secret}}"]
    parent = {
        "step_name": "send",
        "status": "succeeded",
        "attempt": 0,
        "created_at": "2026-10-03T12:00:00Z",
        "for_each_count": 3,
        "output": output,
    }
    assert WorkflowStepResponse.from_dict(parent).to_dict()["output"] == output
    empty = {**parent, "for_each_count": 0, "output": []}
    result = WorkflowStepResponse.from_dict(empty).to_dict()
    assert result["for_each_count"] == 0
    assert result["output"] == []
    item = {**parent, "step_name": "_foreach.c2VuZA.0", "attempt": 1, "for_each_parent": "send", "for_each_index": 0}
    del item["for_each_count"]
    result = WorkflowStepResponse.from_dict(item).to_dict()
    assert result["for_each_index"] == 0
    assert result["for_each_parent"] == "send"
