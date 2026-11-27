from typing import Literal

ManagedPostgresExportState = Literal["failed", "queued", "ready", "running"]

MANAGED_POSTGRES_EXPORT_STATE_VALUES: set[ManagedPostgresExportState] = {
    "failed",
    "queued",
    "ready",
    "running",
}


def check_managed_postgres_export_state(value: str) -> ManagedPostgresExportState:
    if value in MANAGED_POSTGRES_EXPORT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_EXPORT_STATE_VALUES!r}")
