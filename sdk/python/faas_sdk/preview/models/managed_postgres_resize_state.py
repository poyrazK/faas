from typing import Literal

ManagedPostgresResizeState = Literal["pending", "succeeded"]

MANAGED_POSTGRES_RESIZE_STATE_VALUES: set[ManagedPostgresResizeState] = {
    "pending",
    "succeeded",
}


def check_managed_postgres_resize_state(value: str) -> ManagedPostgresResizeState:
    if value in MANAGED_POSTGRES_RESIZE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_RESIZE_STATE_VALUES!r}")
