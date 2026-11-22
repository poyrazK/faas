from typing import Literal

ExecutionPipelineCompletedStepResultValidation = Literal["invalid", "valid"]

EXECUTION_PIPELINE_COMPLETED_STEP_RESULT_VALIDATION_VALUES: set[ExecutionPipelineCompletedStepResultValidation] = {
    "invalid",
    "valid",
}


def check_execution_pipeline_completed_step_result_validation(
    value: str,
) -> ExecutionPipelineCompletedStepResultValidation:
    if value in EXECUTION_PIPELINE_COMPLETED_STEP_RESULT_VALIDATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_COMPLETED_STEP_RESULT_VALIDATION_VALUES!r}"
    )
