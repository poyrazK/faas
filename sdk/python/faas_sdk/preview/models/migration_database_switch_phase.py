from typing import Literal

MigrationDatabaseSwitchPhase = Literal[
    "aborting",
    "cancelled",
    "completed",
    "draining",
    "failed",
    "fencing",
    "restoring",
    "resuming",
    "settling",
    "staging",
    "switching",
    "verifying",
]

MIGRATION_DATABASE_SWITCH_PHASE_VALUES: set[MigrationDatabaseSwitchPhase] = {
    "aborting",
    "cancelled",
    "completed",
    "draining",
    "failed",
    "fencing",
    "restoring",
    "resuming",
    "settling",
    "staging",
    "switching",
    "verifying",
}


def check_migration_database_switch_phase(value: str) -> MigrationDatabaseSwitchPhase:
    if value in MIGRATION_DATABASE_SWITCH_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_DATABASE_SWITCH_PHASE_VALUES!r}")
