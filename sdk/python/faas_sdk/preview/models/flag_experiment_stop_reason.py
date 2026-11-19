from typing import Literal

FlagExperimentStopReason = Literal["guardrail_breach", "manual", "shipped"]

FLAG_EXPERIMENT_STOP_REASON_VALUES: set[FlagExperimentStopReason] = {
    "guardrail_breach",
    "manual",
    "shipped",
}


def check_flag_experiment_stop_reason(value: str) -> FlagExperimentStopReason:
    if value in FLAG_EXPERIMENT_STOP_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_STOP_REASON_VALUES!r}")
