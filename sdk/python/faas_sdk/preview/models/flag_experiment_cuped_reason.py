from typing import Literal

FlagExperimentCupedReason = Literal[
    "no_pre_period_data", "no_pre_period_variance", "not_supported", "off", "pre_period_pending"
]

FLAG_EXPERIMENT_CUPED_REASON_VALUES: set[FlagExperimentCupedReason] = {
    "no_pre_period_data",
    "no_pre_period_variance",
    "not_supported",
    "off",
    "pre_period_pending",
}


def check_flag_experiment_cuped_reason(value: str) -> FlagExperimentCupedReason:
    if value in FLAG_EXPERIMENT_CUPED_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_CUPED_REASON_VALUES!r}")
