from typing import Literal

DataPipelineDesiredState = Literal["deleted", "paused", "running"]

DATA_PIPELINE_DESIRED_STATE_VALUES: set[DataPipelineDesiredState] = {
    "deleted",
    "paused",
    "running",
}


def check_data_pipeline_desired_state(value: str) -> DataPipelineDesiredState:
    if value in DATA_PIPELINE_DESIRED_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_DESIRED_STATE_VALUES!r}")
