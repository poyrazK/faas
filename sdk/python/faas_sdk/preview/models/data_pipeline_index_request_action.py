from typing import Literal

DataPipelineIndexRequestAction = Literal["approve", "discard", "preview"]

DATA_PIPELINE_INDEX_REQUEST_ACTION_VALUES: set[DataPipelineIndexRequestAction] = {
    "approve",
    "discard",
    "preview",
}


def check_data_pipeline_index_request_action(value: str) -> DataPipelineIndexRequestAction:
    if value in DATA_PIPELINE_INDEX_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_INDEX_REQUEST_ACTION_VALUES!r}")
