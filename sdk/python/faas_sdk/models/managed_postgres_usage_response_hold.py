from typing import Literal

ManagedPostgresUsageResponseHold = Literal["compute_ceiling", "egress", "inactive"]

MANAGED_POSTGRES_USAGE_RESPONSE_HOLD_VALUES: set[ManagedPostgresUsageResponseHold] = {
    "compute_ceiling",
    "egress",
    "inactive",
}


def check_managed_postgres_usage_response_hold(value: str) -> ManagedPostgresUsageResponseHold:
    if value in MANAGED_POSTGRES_USAGE_RESPONSE_HOLD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_USAGE_RESPONSE_HOLD_VALUES!r}")
