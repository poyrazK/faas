from typing import Literal

FlagExperimentGuardrailName = Literal["p95_latency", "server_error_rate"]

FLAG_EXPERIMENT_GUARDRAIL_NAME_VALUES: set[FlagExperimentGuardrailName] = {
    "p95_latency",
    "server_error_rate",
}


def check_flag_experiment_guardrail_name(value: str) -> FlagExperimentGuardrailName:
    if value in FLAG_EXPERIMENT_GUARDRAIL_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_GUARDRAIL_NAME_VALUES!r}")
