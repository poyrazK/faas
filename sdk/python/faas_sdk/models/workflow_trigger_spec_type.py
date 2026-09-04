from typing import Literal

WorkflowTriggerSpecType = Literal["manual"]

WORKFLOW_TRIGGER_SPEC_TYPE_VALUES: set[WorkflowTriggerSpecType] = {
    "manual",
}


def check_workflow_trigger_spec_type(value: str) -> WorkflowTriggerSpecType:
    if value in WORKFLOW_TRIGGER_SPEC_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_TRIGGER_SPEC_TYPE_VALUES!r}")
