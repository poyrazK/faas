from typing import Literal

ManagedPostgresImportStatusState = Literal["checked", "done", "failed", "running", "unverified"]

MANAGED_POSTGRES_IMPORT_STATUS_STATE_VALUES: set[ManagedPostgresImportStatusState] = {
    "checked",
    "done",
    "failed",
    "running",
    "unverified",
}


def check_managed_postgres_import_status_state(value: str) -> ManagedPostgresImportStatusState:
    if value in MANAGED_POSTGRES_IMPORT_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_IMPORT_STATUS_STATE_VALUES!r}")
