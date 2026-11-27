from typing import Literal

ManagedPostgresMigrationProgressState = Literal[
    "activated", "catching_up", "copying", "live", "none", "paused", "preparing", "resync_required", "stopped"
]

MANAGED_POSTGRES_MIGRATION_PROGRESS_STATE_VALUES: set[ManagedPostgresMigrationProgressState] = {
    "activated",
    "catching_up",
    "copying",
    "live",
    "none",
    "paused",
    "preparing",
    "resync_required",
    "stopped",
}


def check_managed_postgres_migration_progress_state(value: str) -> ManagedPostgresMigrationProgressState:
    if value in MANAGED_POSTGRES_MIGRATION_PROGRESS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_MIGRATION_PROGRESS_STATE_VALUES!r}")
