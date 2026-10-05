from faas_sdk.models.workflow_join_spec import WorkflowJoinSpec
from faas_sdk.models.workflow_spec import WorkflowSpec
from faas_sdk.models.workflow_step_response import WorkflowStepResponse


def test_join_definition_preserves_explicit_selection_order():
    join = {"output_from": ["receipt", "reminder"]}
    assert WorkflowJoinSpec.from_dict(join).to_dict() == join
    definition = {
        "name": "invoice",
        "steps": [
            {"name": "reminder", "run": "reminder"},
            {"name": "receipt", "run": "receipt"},
            {"name": "merge", "depends_on": ["reminder", "receipt"], "join": join},
        ],
    }
    assert WorkflowSpec.from_dict(definition).to_dict() == definition


def test_join_inspection_preserves_source_and_json_value():
    output = {"source": "receipt", "value": {"n": 9007199254740993, "items": [True, None]}}
    data = {
        "step_name": "merge",
        "status": "succeeded",
        "attempt": 0,
        "created_at": "2026-10-03T12:00:00Z",
        "output": output,
    }
    result = WorkflowStepResponse.from_dict(data).to_dict()
    assert result["output"] == output
    assert result["attempt"] == 0
