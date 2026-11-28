from typing import Literal

ObsRuntimeAppTaskOperationOutcome = Literal[
    "canceled", "cancellation_pending", "capacity", "empty", "error", "lease_lost", "success"
]

OBS_RUNTIME_APP_TASK_OPERATION_OUTCOME_VALUES: set[ObsRuntimeAppTaskOperationOutcome] = {
    "canceled",
    "cancellation_pending",
    "capacity",
    "empty",
    "error",
    "lease_lost",
    "success",
}


def check_obs_runtime_app_task_operation_outcome(value: str) -> ObsRuntimeAppTaskOperationOutcome:
    if value in OBS_RUNTIME_APP_TASK_OPERATION_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_OPERATION_OUTCOME_VALUES!r}")
