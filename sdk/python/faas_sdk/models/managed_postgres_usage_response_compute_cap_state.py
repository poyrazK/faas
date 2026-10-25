from typing import Literal

ManagedPostgresUsageResponseComputeCapState = Literal["healthy", "overage", "suspended", "warning"]

MANAGED_POSTGRES_USAGE_RESPONSE_COMPUTE_CAP_STATE_VALUES: set[ManagedPostgresUsageResponseComputeCapState] = {
    "healthy",
    "overage",
    "suspended",
    "warning",
}


def check_managed_postgres_usage_response_compute_cap_state(value: str) -> ManagedPostgresUsageResponseComputeCapState:
    if value in MANAGED_POSTGRES_USAGE_RESPONSE_COMPUTE_CAP_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_USAGE_RESPONSE_COMPUTE_CAP_STATE_VALUES!r}"
    )
