from typing import Literal

ExecutionPipelineCompletionVersion = Literal["v1"]

EXECUTION_PIPELINE_COMPLETION_VERSION_VALUES: set[ExecutionPipelineCompletionVersion] = {
    "v1",
}


def check_execution_pipeline_completion_version(value: str) -> ExecutionPipelineCompletionVersion:
    if value in EXECUTION_PIPELINE_COMPLETION_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_COMPLETION_VERSION_VALUES!r}")
