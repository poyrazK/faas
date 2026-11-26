from typing import Literal

ManagedPostgresCustomRoleState = Literal["deleting", "pending", "ready"]

MANAGED_POSTGRES_CUSTOM_ROLE_STATE_VALUES: set[ManagedPostgresCustomRoleState] = {
    "deleting",
    "pending",
    "ready",
}


def check_managed_postgres_custom_role_state(value: str) -> ManagedPostgresCustomRoleState:
    if value in MANAGED_POSTGRES_CUSTOM_ROLE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUSTOM_ROLE_STATE_VALUES!r}")
