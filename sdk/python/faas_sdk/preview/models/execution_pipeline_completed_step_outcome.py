from typing import Literal

ExecutionPipelineCompletedStepOutcome = Literal[
    "blocked", "cancelled", "contract_failed", "failed", "not_started", "out_of_memory", "succeeded", "timed_out"
]

EXECUTION_PIPELINE_COMPLETED_STEP_OUTCOME_VALUES: set[ExecutionPipelineCompletedStepOutcome] = {
    "blocked",
    "cancelled",
    "contract_failed",
    "failed",
    "not_started",
    "out_of_memory",
    "succeeded",
    "timed_out",
}


def check_execution_pipeline_completed_step_outcome(value: str) -> ExecutionPipelineCompletedStepOutcome:
    if value in EXECUTION_PIPELINE_COMPLETED_STEP_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_COMPLETED_STEP_OUTCOME_VALUES!r}")
