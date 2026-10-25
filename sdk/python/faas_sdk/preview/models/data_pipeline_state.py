from typing import Literal

DataPipelineState = Literal[
    "blocked", "catching_up", "copying", "deleted", "deleting", "live", "paused", "resync_required", "validating"
]

DATA_PIPELINE_STATE_VALUES: set[DataPipelineState] = {
    "blocked",
    "catching_up",
    "copying",
    "deleted",
    "deleting",
    "live",
    "paused",
    "resync_required",
    "validating",
}


def check_data_pipeline_state(value: str) -> DataPipelineState:
    if value in DATA_PIPELINE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_STATE_VALUES!r}")
