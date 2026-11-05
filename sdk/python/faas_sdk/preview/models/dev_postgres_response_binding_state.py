from typing import Literal

DevPostgresResponseBindingState = Literal["deleted", "deleting", "failed", "provisioning", "ready"]

DEV_POSTGRES_RESPONSE_BINDING_STATE_VALUES: set[DevPostgresResponseBindingState] = {
    "deleted",
    "deleting",
    "failed",
    "provisioning",
    "ready",
}


def check_dev_postgres_response_binding_state(value: str) -> DevPostgresResponseBindingState:
    if value in DEV_POSTGRES_RESPONSE_BINDING_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_POSTGRES_RESPONSE_BINDING_STATE_VALUES!r}")
