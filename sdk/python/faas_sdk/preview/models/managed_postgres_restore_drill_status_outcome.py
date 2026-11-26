from typing import Literal

ManagedPostgresRestoreDrillStatusOutcome = Literal["failed", "queued", "running", "skipped", "succeeded"]

MANAGED_POSTGRES_RESTORE_DRILL_STATUS_OUTCOME_VALUES: set[ManagedPostgresRestoreDrillStatusOutcome] = {
    "failed",
    "queued",
    "running",
    "skipped",
    "succeeded",
}


def check_managed_postgres_restore_drill_status_outcome(value: str) -> ManagedPostgresRestoreDrillStatusOutcome:
    if value in MANAGED_POSTGRES_RESTORE_DRILL_STATUS_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_RESTORE_DRILL_STATUS_OUTCOME_VALUES!r}"
    )
