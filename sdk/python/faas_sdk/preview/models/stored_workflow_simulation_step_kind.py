from typing import Literal

StoredWorkflowSimulationStepKind = Literal[
    "callback_wait", "condition_wait", "duration_wait", "event_wait", "for_each", "join", "outbound", "path", "run"
]

STORED_WORKFLOW_SIMULATION_STEP_KIND_VALUES: set[StoredWorkflowSimulationStepKind] = {
    "callback_wait",
    "condition_wait",
    "duration_wait",
    "event_wait",
    "for_each",
    "join",
    "outbound",
    "path",
    "run",
}


def check_stored_workflow_simulation_step_kind(value: str) -> StoredWorkflowSimulationStepKind:
    if value in STORED_WORKFLOW_SIMULATION_STEP_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_SIMULATION_STEP_KIND_VALUES!r}")
