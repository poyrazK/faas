from typing import Literal

FlagExperimentGuardrailVariantStatus = Literal["breached", "insufficient_data", "ok"]

FLAG_EXPERIMENT_GUARDRAIL_VARIANT_STATUS_VALUES: set[FlagExperimentGuardrailVariantStatus] = {
    "breached",
    "insufficient_data",
    "ok",
}


def check_flag_experiment_guardrail_variant_status(value: str) -> FlagExperimentGuardrailVariantStatus:
    if value in FLAG_EXPERIMENT_GUARDRAIL_VARIANT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_GUARDRAIL_VARIANT_STATUS_VALUES!r}")
