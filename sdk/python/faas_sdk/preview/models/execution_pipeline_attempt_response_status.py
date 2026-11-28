from typing import Literal

ExecutionPipelineAttemptResponseStatus = Literal[
    "cancelled", "failed", "out_of_memory", "queued", "restoring", "running", "succeeded", "timed_out"
]

EXECUTION_PIPELINE_ATTEMPT_RESPONSE_STATUS_VALUES: set[ExecutionPipelineAttemptResponseStatus] = {
    "cancelled",
    "failed",
    "out_of_memory",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
}


def check_execution_pipeline_attempt_response_status(value: str) -> ExecutionPipelineAttemptResponseStatus:
    if value in EXECUTION_PIPELINE_ATTEMPT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_ATTEMPT_RESPONSE_STATUS_VALUES!r}"
    )
