from typing import Literal

FlagExperimentStatus = Literal["draft", "running", "stopped"]

FLAG_EXPERIMENT_STATUS_VALUES: set[FlagExperimentStatus] = {
    "draft",
    "running",
    "stopped",
}


def check_flag_experiment_status(value: str) -> FlagExperimentStatus:
    if value in FLAG_EXPERIMENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_STATUS_VALUES!r}")
