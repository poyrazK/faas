from typing import Literal

ManagedPostgresUsageResponseEgressState = Literal["healthy", "held", "overage", "warning"]

MANAGED_POSTGRES_USAGE_RESPONSE_EGRESS_STATE_VALUES: set[ManagedPostgresUsageResponseEgressState] = {
    "healthy",
    "held",
    "overage",
    "warning",
}


def check_managed_postgres_usage_response_egress_state(value: str) -> ManagedPostgresUsageResponseEgressState:
    if value in MANAGED_POSTGRES_USAGE_RESPONSE_EGRESS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_USAGE_RESPONSE_EGRESS_STATE_VALUES!r}"
    )
