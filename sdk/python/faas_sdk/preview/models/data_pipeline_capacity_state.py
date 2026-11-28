from typing import Literal

DataPipelineCapacityState = Literal["insufficient_capacity", "needs_qualification", "ready"]

DATA_PIPELINE_CAPACITY_STATE_VALUES: set[DataPipelineCapacityState] = {
    "insufficient_capacity",
    "needs_qualification",
    "ready",
}


def check_data_pipeline_capacity_state(value: str) -> DataPipelineCapacityState:
    if value in DATA_PIPELINE_CAPACITY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_CAPACITY_STATE_VALUES!r}")
