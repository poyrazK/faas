from typing import Literal

ExecutionPipelineCompletedAttemptStatus = Literal["cancelled", "failed", "out_of_memory", "succeeded", "timed_out"]

EXECUTION_PIPELINE_COMPLETED_ATTEMPT_STATUS_VALUES: set[ExecutionPipelineCompletedAttemptStatus] = {
    "cancelled",
    "failed",
    "out_of_memory",
    "succeeded",
    "timed_out",
}


def check_execution_pipeline_completed_attempt_status(value: str) -> ExecutionPipelineCompletedAttemptStatus:
    if value in EXECUTION_PIPELINE_COMPLETED_ATTEMPT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_COMPLETED_ATTEMPT_STATUS_VALUES!r}"
    )
