from typing import Literal

FlagExperimentGuardrailStatus = Literal["breached", "insufficient_data", "ok"]

FLAG_EXPERIMENT_GUARDRAIL_STATUS_VALUES: set[FlagExperimentGuardrailStatus] = {
    "breached",
    "insufficient_data",
    "ok",
}


def check_flag_experiment_guardrail_status(value: str) -> FlagExperimentGuardrailStatus:
    if value in FLAG_EXPERIMENT_GUARDRAIL_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_GUARDRAIL_STATUS_VALUES!r}")
