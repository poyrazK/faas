from typing import Literal

ExecutionPipelineProgressStatus = Literal["cancelled", "deadline_exceeded", "failed", "queued", "running", "succeeded"]

EXECUTION_PIPELINE_PROGRESS_STATUS_VALUES: set[ExecutionPipelineProgressStatus] = {
    "cancelled",
    "deadline_exceeded",
    "failed",
    "queued",
    "running",
    "succeeded",
}


def check_execution_pipeline_progress_status(value: str) -> ExecutionPipelineProgressStatus:
    if value in EXECUTION_PIPELINE_PROGRESS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_PROGRESS_STATUS_VALUES!r}")
