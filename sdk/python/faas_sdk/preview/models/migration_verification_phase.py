from typing import Literal

MigrationVerificationPhase = Literal[
    "cold_dependencies",
    "cold_smoke",
    "complete",
    "dependencies",
    "drain",
    "park",
    "restoring",
    "startup",
    "wake",
    "waking",
]

MIGRATION_VERIFICATION_PHASE_VALUES: set[MigrationVerificationPhase] = {
    "cold_dependencies",
    "cold_smoke",
    "complete",
    "dependencies",
    "drain",
    "park",
    "restoring",
    "startup",
    "wake",
    "waking",
}


def check_migration_verification_phase(value: str) -> MigrationVerificationPhase:
    if value in MIGRATION_VERIFICATION_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_VERIFICATION_PHASE_VALUES!r}")
