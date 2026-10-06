from typing import Literal

ManagedPostgresRecoveryStatusStatus = Literal["available", "limits_known", "unavailable", "unknown", "unsupported"]

MANAGED_POSTGRES_RECOVERY_STATUS_STATUS_VALUES: set[ManagedPostgresRecoveryStatusStatus] = {
    "available",
    "limits_known",
    "unavailable",
    "unknown",
    "unsupported",
}


def check_managed_postgres_recovery_status_status(value: str) -> ManagedPostgresRecoveryStatusStatus:
    if value in MANAGED_POSTGRES_RECOVERY_STATUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_RECOVERY_STATUS_STATUS_VALUES!r}")
