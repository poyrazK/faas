from typing import Literal

MigrationCutoverStatus = Literal[
    "attention", "blocked", "cancelled", "checking", "completed", "prepared", "ready", "restored"
]

MIGRATION_CUTOVER_STATUS_VALUES: set[MigrationCutoverStatus] = {
    "attention",
    "blocked",
    "cancelled",
    "checking",
    "completed",
    "prepared",
    "ready",
    "restored",
}


def check_migration_cutover_status(value: str) -> MigrationCutoverStatus:
    if value in MIGRATION_CUTOVER_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_CUTOVER_STATUS_VALUES!r}")
