from typing import Literal

FlagExperimentVariantResultStatus = Literal[
    "insufficient_data", "not_significant", "sample_ratio_mismatch", "significant"
]

FLAG_EXPERIMENT_VARIANT_RESULT_STATUS_VALUES: set[FlagExperimentVariantResultStatus] = {
    "insufficient_data",
    "not_significant",
    "sample_ratio_mismatch",
    "significant",
}


def check_flag_experiment_variant_result_status(value: str) -> FlagExperimentVariantResultStatus:
    if value in FLAG_EXPERIMENT_VARIANT_RESULT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_VARIANT_RESULT_STATUS_VALUES!r}")
