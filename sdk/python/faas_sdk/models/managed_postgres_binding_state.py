from typing import Literal

ManagedPostgresBindingState = Literal["deleted", "deleting", "failed", "provisioning", "ready"]

MANAGED_POSTGRES_BINDING_STATE_VALUES: set[ManagedPostgresBindingState] = {
    "deleted",
    "deleting",
    "failed",
    "provisioning",
    "ready",
}


def check_managed_postgres_binding_state(value: str) -> ManagedPostgresBindingState:
    if value in MANAGED_POSTGRES_BINDING_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_BINDING_STATE_VALUES!r}")
