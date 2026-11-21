from typing import Literal

ExecutionPipelineStepProgressReason = Literal[
    "admission_refused",
    "admissions_disabled",
    "budget_concurrency",
    "cancelling",
    "capacity",
    "dependencies",
    "fail_fast",
    "parallel_limit",
    "pending",
    "plan_identity",
    "retry_backoff",
]

EXECUTION_PIPELINE_STEP_PROGRESS_REASON_VALUES: set[ExecutionPipelineStepProgressReason] = {
    "admission_refused",
    "admissions_disabled",
    "budget_concurrency",
    "cancelling",
    "capacity",
    "dependencies",
    "fail_fast",
    "parallel_limit",
    "pending",
    "plan_identity",
    "retry_backoff",
}


def check_execution_pipeline_step_progress_reason(value: str) -> ExecutionPipelineStepProgressReason:
    if value in EXECUTION_PIPELINE_STEP_PROGRESS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_STEP_PROGRESS_REASON_VALUES!r}")
