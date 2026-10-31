from typing import Literal

ManagedPostgresHealthLastErrorCode = Literal[
    "backend_unavailable",
    "observation_invalid",
    "observer_unsupported",
    "provider_failed",
    "provider_unavailable",
    "resource_missing",
    "spec_mismatch",
]

MANAGED_POSTGRES_HEALTH_LAST_ERROR_CODE_VALUES: set[ManagedPostgresHealthLastErrorCode] = {
    "backend_unavailable",
    "observation_invalid",
    "observer_unsupported",
    "provider_failed",
    "provider_unavailable",
    "resource_missing",
    "spec_mismatch",
}


def check_managed_postgres_health_last_error_code(value: str) -> ManagedPostgresHealthLastErrorCode:
    if value in MANAGED_POSTGRES_HEALTH_LAST_ERROR_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_HEALTH_LAST_ERROR_CODE_VALUES!r}")
