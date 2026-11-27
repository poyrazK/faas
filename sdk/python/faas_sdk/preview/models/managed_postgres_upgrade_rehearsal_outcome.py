from typing import Literal

ManagedPostgresUpgradeRehearsalOutcome = Literal["cleaning_up", "failed", "queued", "running", "succeeded"]

MANAGED_POSTGRES_UPGRADE_REHEARSAL_OUTCOME_VALUES: set[ManagedPostgresUpgradeRehearsalOutcome] = {
    "cleaning_up",
    "failed",
    "queued",
    "running",
    "succeeded",
}


def check_managed_postgres_upgrade_rehearsal_outcome(value: str) -> ManagedPostgresUpgradeRehearsalOutcome:
    if value in MANAGED_POSTGRES_UPGRADE_REHEARSAL_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_UPGRADE_REHEARSAL_OUTCOME_VALUES!r}"
    )
