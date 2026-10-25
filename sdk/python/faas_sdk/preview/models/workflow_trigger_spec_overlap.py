from typing import Literal

WorkflowTriggerSpecOverlap = Literal["allow", "skip"]

WORKFLOW_TRIGGER_SPEC_OVERLAP_VALUES: set[WorkflowTriggerSpecOverlap] = {
    "allow",
    "skip",
}


def check_workflow_trigger_spec_overlap(value: str) -> WorkflowTriggerSpecOverlap:
    if value in WORKFLOW_TRIGGER_SPEC_OVERLAP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_TRIGGER_SPEC_OVERLAP_VALUES!r}")
