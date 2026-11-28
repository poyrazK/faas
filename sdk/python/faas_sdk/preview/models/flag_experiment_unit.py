from typing import Literal

FlagExperimentUnit = Literal["customer", "subject"]

FLAG_EXPERIMENT_UNIT_VALUES: set[FlagExperimentUnit] = {
    "customer",
    "subject",
}


def check_flag_experiment_unit(value: str) -> FlagExperimentUnit:
    if value in FLAG_EXPERIMENT_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_UNIT_VALUES!r}")
