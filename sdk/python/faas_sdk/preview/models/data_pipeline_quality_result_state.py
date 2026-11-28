from typing import Literal

DataPipelineQualityResultState = Literal["failed", "inconclusive", "passed", "unknown"]

DATA_PIPELINE_QUALITY_RESULT_STATE_VALUES: set[DataPipelineQualityResultState] = {
    "failed",
    "inconclusive",
    "passed",
    "unknown",
}


def check_data_pipeline_quality_result_state(value: str) -> DataPipelineQualityResultState:
    if value in DATA_PIPELINE_QUALITY_RESULT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_QUALITY_RESULT_STATE_VALUES!r}")
