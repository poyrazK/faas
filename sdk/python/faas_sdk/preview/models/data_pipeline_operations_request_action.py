from typing import Literal

DataPipelineOperationsRequestAction = Literal["configure", "run_quality", "run_snapshot"]

DATA_PIPELINE_OPERATIONS_REQUEST_ACTION_VALUES: set[DataPipelineOperationsRequestAction] = {
    "configure",
    "run_quality",
    "run_snapshot",
}


def check_data_pipeline_operations_request_action(value: str) -> DataPipelineOperationsRequestAction:
    if value in DATA_PIPELINE_OPERATIONS_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_OPERATIONS_REQUEST_ACTION_VALUES!r}")
