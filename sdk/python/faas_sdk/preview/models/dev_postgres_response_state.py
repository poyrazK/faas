from typing import Literal

DevPostgresResponseState = Literal["deleted", "deleting", "failed", "provisioning", "ready", "updating"]

DEV_POSTGRES_RESPONSE_STATE_VALUES: set[DevPostgresResponseState] = {
    "deleted",
    "deleting",
    "failed",
    "provisioning",
    "ready",
    "updating",
}


def check_dev_postgres_response_state(value: str) -> DevPostgresResponseState:
    if value in DEV_POSTGRES_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_POSTGRES_RESPONSE_STATE_VALUES!r}")
