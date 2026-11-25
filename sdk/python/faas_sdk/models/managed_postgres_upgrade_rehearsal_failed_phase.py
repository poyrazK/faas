from typing import Literal

ManagedPostgresUpgradeRehearsalFailedPhase = Literal["mark", "restore", "upgrade", "verify"]

MANAGED_POSTGRES_UPGRADE_REHEARSAL_FAILED_PHASE_VALUES: set[ManagedPostgresUpgradeRehearsalFailedPhase] = {
    "mark",
    "restore",
    "upgrade",
    "verify",
}


def check_managed_postgres_upgrade_rehearsal_failed_phase(value: str) -> ManagedPostgresUpgradeRehearsalFailedPhase:
    if value in MANAGED_POSTGRES_UPGRADE_REHEARSAL_FAILED_PHASE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_UPGRADE_REHEARSAL_FAILED_PHASE_VALUES!r}"
    )
