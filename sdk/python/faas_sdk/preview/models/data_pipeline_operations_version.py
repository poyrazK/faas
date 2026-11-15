from typing import Literal

DataPipelineOperationsVersion = Literal[1]

DATA_PIPELINE_OPERATIONS_VERSION_VALUES: set[DataPipelineOperationsVersion] = {
    1,
}


def check_data_pipeline_operations_version(value: int) -> DataPipelineOperationsVersion:
    if value in DATA_PIPELINE_OPERATIONS_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_OPERATIONS_VERSION_VALUES!r}")
