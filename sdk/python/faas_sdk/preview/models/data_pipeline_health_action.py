from typing import Literal

DataPipelineHealthAction = Literal["resume", "resync", "rotate_credentials"]

DATA_PIPELINE_HEALTH_ACTION_VALUES: set[DataPipelineHealthAction] = {
    "resume",
    "resync",
    "rotate_credentials",
}


def check_data_pipeline_health_action(value: str) -> DataPipelineHealthAction:
    if value in DATA_PIPELINE_HEALTH_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_HEALTH_ACTION_VALUES!r}")
