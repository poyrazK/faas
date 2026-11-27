from typing import Literal

StoredWorkflowSimulationAttemptOutcome = Literal["failure", "success", "timeout"]

STORED_WORKFLOW_SIMULATION_ATTEMPT_OUTCOME_VALUES: set[StoredWorkflowSimulationAttemptOutcome] = {
    "failure",
    "success",
    "timeout",
}


def check_stored_workflow_simulation_attempt_outcome(value: str) -> StoredWorkflowSimulationAttemptOutcome:
    if value in STORED_WORKFLOW_SIMULATION_ATTEMPT_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_SIMULATION_ATTEMPT_OUTCOME_VALUES!r}"
    )
