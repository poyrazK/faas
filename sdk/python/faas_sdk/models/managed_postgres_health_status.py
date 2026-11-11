from typing import Literal

ManagedPostgresHealthStatus = Literal["degraded", "disabled", "healthy", "stale", "unknown"]

MANAGED_POSTGRES_HEALTH_STATUS_VALUES: set[ManagedPostgresHealthStatus] = {
    "degraded",
    "disabled",
    "healthy",
    "stale",
    "unknown",
}


def check_managed_postgres_health_status(value: str) -> ManagedPostgresHealthStatus:
    if value in MANAGED_POSTGRES_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_HEALTH_STATUS_VALUES!r}")
