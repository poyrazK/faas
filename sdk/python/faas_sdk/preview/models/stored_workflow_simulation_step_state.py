from typing import Literal

StoredWorkflowSimulationStepState = Literal[
    "blocked",
    "dead",
    "error",
    "expanded",
    "failed",
    "mocked",
    "resolved",
    "skipped",
    "timed_out",
    "would_execute",
    "would_retry",
    "would_wait",
]

STORED_WORKFLOW_SIMULATION_STEP_STATE_VALUES: set[StoredWorkflowSimulationStepState] = {
    "blocked",
    "dead",
    "error",
    "expanded",
    "failed",
    "mocked",
    "resolved",
    "skipped",
    "timed_out",
    "would_execute",
    "would_retry",
    "would_wait",
}


def check_stored_workflow_simulation_step_state(value: str) -> StoredWorkflowSimulationStepState:
    if value in STORED_WORKFLOW_SIMULATION_STEP_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_SIMULATION_STEP_STATE_VALUES!r}")
