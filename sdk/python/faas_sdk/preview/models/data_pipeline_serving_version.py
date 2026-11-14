from typing import Literal

DataPipelineServingVersion = Literal[1]

DATA_PIPELINE_SERVING_VERSION_VALUES: set[DataPipelineServingVersion] = {
    1,
}


def check_data_pipeline_serving_version(value: int) -> DataPipelineServingVersion:
    if value in DATA_PIPELINE_SERVING_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_SERVING_VERSION_VALUES!r}")
