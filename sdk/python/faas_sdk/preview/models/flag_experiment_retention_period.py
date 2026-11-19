from typing import Literal

FlagExperimentRetentionPeriod = Literal["day", "week"]

FLAG_EXPERIMENT_RETENTION_PERIOD_VALUES: set[FlagExperimentRetentionPeriod] = {
    "day",
    "week",
}


def check_flag_experiment_retention_period(value: str) -> FlagExperimentRetentionPeriod:
    if value in FLAG_EXPERIMENT_RETENTION_PERIOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_RETENTION_PERIOD_VALUES!r}")
