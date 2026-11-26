from typing import Literal

ManagedPostgresActivityCauseCause = Literal["app_start", "app_wake", "cron", "external", "request", "running_app"]

MANAGED_POSTGRES_ACTIVITY_CAUSE_CAUSE_VALUES: set[ManagedPostgresActivityCauseCause] = {
    "app_start",
    "app_wake",
    "cron",
    "external",
    "request",
    "running_app",
}


def check_managed_postgres_activity_cause_cause(value: str) -> ManagedPostgresActivityCauseCause:
    if value in MANAGED_POSTGRES_ACTIVITY_CAUSE_CAUSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_ACTIVITY_CAUSE_CAUSE_VALUES!r}")
