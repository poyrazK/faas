from typing import Literal

ManagedPostgresBackupScheduleState = Literal["pending", "running", "scheduled"]

MANAGED_POSTGRES_BACKUP_SCHEDULE_STATE_VALUES: set[ManagedPostgresBackupScheduleState] = {
    "pending",
    "running",
    "scheduled",
}


def check_managed_postgres_backup_schedule_state(value: str) -> ManagedPostgresBackupScheduleState:
    if value in MANAGED_POSTGRES_BACKUP_SCHEDULE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_BACKUP_SCHEDULE_STATE_VALUES!r}")
