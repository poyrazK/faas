from typing import Literal

ManagedPostgresHealthProviderStatus = Literal["deleting", "failed", "missing", "pending", "ready", "unknown"]

MANAGED_POSTGRES_HEALTH_PROVIDER_STATUS_VALUES: set[ManagedPostgresHealthProviderStatus] = {
    "deleting",
    "failed",
    "missing",
    "pending",
    "ready",
    "unknown",
}


def check_managed_postgres_health_provider_status(value: str) -> ManagedPostgresHealthProviderStatus:
    if value in MANAGED_POSTGRES_HEALTH_PROVIDER_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_HEALTH_PROVIDER_STATUS_VALUES!r}")
