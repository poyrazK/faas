from typing import Literal

DataPipelineQualityStatusState = Literal["failed", "inconclusive", "passed", "unknown"]

DATA_PIPELINE_QUALITY_STATUS_STATE_VALUES: set[DataPipelineQualityStatusState] = {
    "failed",
    "inconclusive",
    "passed",
    "unknown",
}


def check_data_pipeline_quality_status_state(value: str) -> DataPipelineQualityStatusState:
    if value in DATA_PIPELINE_QUALITY_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_QUALITY_STATUS_STATE_VALUES!r}")
