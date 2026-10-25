from typing import Literal

WorkflowInitialObservationFailureCode = Literal["invalid_observation_data", "invalid_recovery_identity"]

WORKFLOW_INITIAL_OBSERVATION_FAILURE_CODE_VALUES: set[WorkflowInitialObservationFailureCode] = {
    "invalid_observation_data",
    "invalid_recovery_identity",
}


def check_workflow_initial_observation_failure_code(value: str) -> WorkflowInitialObservationFailureCode:
    if value in WORKFLOW_INITIAL_OBSERVATION_FAILURE_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_INITIAL_OBSERVATION_FAILURE_CODE_VALUES!r}")
