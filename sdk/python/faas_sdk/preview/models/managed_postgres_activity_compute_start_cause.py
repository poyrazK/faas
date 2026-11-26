from typing import Literal

ManagedPostgresActivityComputeStartCause = Literal[
    "app_start", "app_wake", "cron", "external", "request", "running_app"
]

MANAGED_POSTGRES_ACTIVITY_COMPUTE_START_CAUSE_VALUES: set[ManagedPostgresActivityComputeStartCause] = {
    "app_start",
    "app_wake",
    "cron",
    "external",
    "request",
    "running_app",
}


def check_managed_postgres_activity_compute_start_cause(value: str) -> ManagedPostgresActivityComputeStartCause:
    if value in MANAGED_POSTGRES_ACTIVITY_COMPUTE_START_CAUSE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_ACTIVITY_COMPUTE_START_CAUSE_VALUES!r}"
    )
