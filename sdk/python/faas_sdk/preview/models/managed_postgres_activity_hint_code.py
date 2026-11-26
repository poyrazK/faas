from typing import Literal

ManagedPostgresActivityHintCode = Literal[
    "app_start_wakes_database",
    "app_wake_starts_database",
    "cron_wakes_database",
    "external_client",
    "running_app_holds_connections",
]

MANAGED_POSTGRES_ACTIVITY_HINT_CODE_VALUES: set[ManagedPostgresActivityHintCode] = {
    "app_start_wakes_database",
    "app_wake_starts_database",
    "cron_wakes_database",
    "external_client",
    "running_app_holds_connections",
}


def check_managed_postgres_activity_hint_code(value: str) -> ManagedPostgresActivityHintCode:
    if value in MANAGED_POSTGRES_ACTIVITY_HINT_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_ACTIVITY_HINT_CODE_VALUES!r}")
