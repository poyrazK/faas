from typing import Literal

WorkflowTriggerSpecCatchUp = Literal["latest", "skip"]

WORKFLOW_TRIGGER_SPEC_CATCH_UP_VALUES: set[WorkflowTriggerSpecCatchUp] = {
    "latest",
    "skip",
}


def check_workflow_trigger_spec_catch_up(value: str) -> WorkflowTriggerSpecCatchUp:
    if value in WORKFLOW_TRIGGER_SPEC_CATCH_UP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_TRIGGER_SPEC_CATCH_UP_VALUES!r}")
