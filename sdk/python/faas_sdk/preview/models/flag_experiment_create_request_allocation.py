from typing import Literal

FlagExperimentCreateRequestAllocation = Literal["bandit", "fixed"]

FLAG_EXPERIMENT_CREATE_REQUEST_ALLOCATION_VALUES: set[FlagExperimentCreateRequestAllocation] = {
    "bandit",
    "fixed",
}


def check_flag_experiment_create_request_allocation(value: str) -> FlagExperimentCreateRequestAllocation:
    if value in FLAG_EXPERIMENT_CREATE_REQUEST_ALLOCATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_CREATE_REQUEST_ALLOCATION_VALUES!r}")
