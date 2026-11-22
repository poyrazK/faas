from typing import Literal

ExecutionPipelineEventType = Literal["pipeline", "step"]

EXECUTION_PIPELINE_EVENT_TYPE_VALUES: set[ExecutionPipelineEventType] = {
    "pipeline",
    "step",
}


def check_execution_pipeline_event_type(value: str) -> ExecutionPipelineEventType:
    if value in EXECUTION_PIPELINE_EVENT_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_PIPELINE_EVENT_TYPE_VALUES!r}")
