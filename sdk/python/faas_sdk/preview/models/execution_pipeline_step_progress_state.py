from typing import Literal

ExecutionPipelineStepProgressState = Literal[
    "blocked",
    "cancelled",
    "contract_failed",
    "failed",
    "out_of_memory",
    "queued",
    "restoring",
    "retrying",
    "reused",
    "running",
    "succeeded",
    "timed_out",
]

EXECUTION_PIPELINE_STEP_PROGRESS_STATE_VALUES: set[ExecutionPipelineStepProgressState] = {
    "blocked",
    "cancelled",
    "contract_failed",
    "failed",
    "out_of_memory",
    "queued",
    "restoring",
    "retrying",
    "reused",
    "running",
    "succeeded",
    "timed_out",
}


def check_execution_pipeline_step_progress_state(value: str) -> ExecutionPipelineStepProgressState:
    if value in EXECUTION_PIPELINE_STEP_PROGRESS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_STEP_PROGRESS_STATE_VALUES!r}")
