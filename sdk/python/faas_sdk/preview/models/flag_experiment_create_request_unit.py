from typing import Literal

FlagExperimentCreateRequestUnit = Literal["customer", "subject"]

FLAG_EXPERIMENT_CREATE_REQUEST_UNIT_VALUES: set[FlagExperimentCreateRequestUnit] = {
    "customer",
    "subject",
}


def check_flag_experiment_create_request_unit(value: str) -> FlagExperimentCreateRequestUnit:
    if value in FLAG_EXPERIMENT_CREATE_REQUEST_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_CREATE_REQUEST_UNIT_VALUES!r}")
