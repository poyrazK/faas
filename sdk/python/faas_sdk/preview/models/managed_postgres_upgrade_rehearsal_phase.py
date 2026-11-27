from typing import Literal

ManagedPostgresUpgradeRehearsalPhase = Literal["cleanup", "mark", "restore", "upgrade", "verify"]

MANAGED_POSTGRES_UPGRADE_REHEARSAL_PHASE_VALUES: set[ManagedPostgresUpgradeRehearsalPhase] = {
    "cleanup",
    "mark",
    "restore",
    "upgrade",
    "verify",
}


def check_managed_postgres_upgrade_rehearsal_phase(value: str) -> ManagedPostgresUpgradeRehearsalPhase:
    if value in MANAGED_POSTGRES_UPGRADE_REHEARSAL_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_UPGRADE_REHEARSAL_PHASE_VALUES!r}")
