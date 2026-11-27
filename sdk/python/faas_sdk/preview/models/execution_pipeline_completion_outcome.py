from typing import Literal

ExecutionPipelineCompletionOutcome = Literal["cancelled", "deadline_exceeded", "failed", "succeeded"]

EXECUTION_PIPELINE_COMPLETION_OUTCOME_VALUES: set[ExecutionPipelineCompletionOutcome] = {
    "cancelled",
    "deadline_exceeded",
    "failed",
    "succeeded",
}


def check_execution_pipeline_completion_outcome(value: str) -> ExecutionPipelineCompletionOutcome:
    if value in EXECUTION_PIPELINE_COMPLETION_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_COMPLETION_OUTCOME_VALUES!r}")
