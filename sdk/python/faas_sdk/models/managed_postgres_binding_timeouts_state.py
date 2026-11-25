from typing import Literal

ManagedPostgresBindingTimeoutsState = Literal["applied", "default", "pending", "refreshing"]

MANAGED_POSTGRES_BINDING_TIMEOUTS_STATE_VALUES: set[ManagedPostgresBindingTimeoutsState] = {
    "applied",
    "default",
    "pending",
    "refreshing",
}


def check_managed_postgres_binding_timeouts_state(value: str) -> ManagedPostgresBindingTimeoutsState:
    if value in MANAGED_POSTGRES_BINDING_TIMEOUTS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_BINDING_TIMEOUTS_STATE_VALUES!r}")
