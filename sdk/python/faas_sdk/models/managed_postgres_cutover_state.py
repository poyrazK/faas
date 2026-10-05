from typing import Literal

ManagedPostgresCutoverState = Literal["cancelled", "cancelling", "prepared", "preparing", "verified", "verifying"]

MANAGED_POSTGRES_CUTOVER_STATE_VALUES: set[ManagedPostgresCutoverState] = {
    "cancelled",
    "cancelling",
    "prepared",
    "preparing",
    "verified",
    "verifying",
}


def check_managed_postgres_cutover_state(value: str) -> ManagedPostgresCutoverState:
    if value in MANAGED_POSTGRES_CUTOVER_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_STATE_VALUES!r}")
