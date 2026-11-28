from typing import Literal

ObsRuntimeAppTaskOperationsReason = Literal[
    "comparison_not_requested",
    "incomplete_operation_evidence",
    "operation_evidence_unavailable",
    "operation_increases_observed",
]

OBS_RUNTIME_APP_TASK_OPERATIONS_REASON_VALUES: set[ObsRuntimeAppTaskOperationsReason] = {
    "comparison_not_requested",
    "incomplete_operation_evidence",
    "operation_evidence_unavailable",
    "operation_increases_observed",
}


def check_obs_runtime_app_task_operations_reason(value: str) -> ObsRuntimeAppTaskOperationsReason:
    if value in OBS_RUNTIME_APP_TASK_OPERATIONS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_OPERATIONS_REASON_VALUES!r}")
