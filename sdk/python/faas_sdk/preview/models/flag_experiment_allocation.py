from typing import Literal

FlagExperimentAllocation = Literal["bandit", "fixed"]

FLAG_EXPERIMENT_ALLOCATION_VALUES: set[FlagExperimentAllocation] = {
    "bandit",
    "fixed",
}


def check_flag_experiment_allocation(value: str) -> FlagExperimentAllocation:
    if value in FLAG_EXPERIMENT_ALLOCATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_ALLOCATION_VALUES!r}")
