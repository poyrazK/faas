from typing import Literal

DataPipelineVerification = Literal["inconclusive", "mismatch", "passed", "pending", "unknown"]

DATA_PIPELINE_VERIFICATION_VALUES: set[DataPipelineVerification] = {
    "inconclusive",
    "mismatch",
    "passed",
    "pending",
    "unknown",
}


def check_data_pipeline_verification(value: str) -> DataPipelineVerification:
    if value in DATA_PIPELINE_VERIFICATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_PIPELINE_VERIFICATION_VALUES!r}")
