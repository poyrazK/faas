from typing import Literal

DataPipelineHealthState = Literal["attention", "healthy", "inactive", "pending", "unknown"]

DATA_PIPELINE_HEALTH_STATE_VALUES: set[DataPipelineHealthState] = {
    "attention",
    "healthy",
    "inactive",
    "pending",
    "unknown",
}


def check_data_pipeline_health_state(value: str) -> DataPipelineHealthState:
    if value in DATA_PIPELINE_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_HEALTH_STATE_VALUES!r}")
