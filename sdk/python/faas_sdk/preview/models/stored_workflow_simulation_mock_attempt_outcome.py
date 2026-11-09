from typing import Literal

StoredWorkflowSimulationMockAttemptOutcome = Literal["failure", "success", "timeout"]

STORED_WORKFLOW_SIMULATION_MOCK_ATTEMPT_OUTCOME_VALUES: set[StoredWorkflowSimulationMockAttemptOutcome] = {
    "failure",
    "success",
    "timeout",
}


def check_stored_workflow_simulation_mock_attempt_outcome(value: str) -> StoredWorkflowSimulationMockAttemptOutcome:
    if value in STORED_WORKFLOW_SIMULATION_MOCK_ATTEMPT_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_SIMULATION_MOCK_ATTEMPT_OUTCOME_VALUES!r}"
    )
