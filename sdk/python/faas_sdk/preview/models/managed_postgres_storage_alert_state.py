from typing import Literal

ManagedPostgresStorageAlertState = Literal["low_space", "read_only", "recovered"]

MANAGED_POSTGRES_STORAGE_ALERT_STATE_VALUES: set[ManagedPostgresStorageAlertState] = {
    "low_space",
    "read_only",
    "recovered",
}


def check_managed_postgres_storage_alert_state(value: str) -> ManagedPostgresStorageAlertState:
    if value in MANAGED_POSTGRES_STORAGE_ALERT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_STORAGE_ALERT_STATE_VALUES!r}")
