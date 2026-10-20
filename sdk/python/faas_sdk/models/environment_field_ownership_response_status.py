from typing import Literal

EnvironmentFieldOwnershipResponseStatus = Literal["claimed", "released", "unscoped"]

ENVIRONMENT_FIELD_OWNERSHIP_RESPONSE_STATUS_VALUES: set[EnvironmentFieldOwnershipResponseStatus] = {
    "claimed",
    "released",
    "unscoped",
}


def check_environment_field_ownership_response_status(value: str) -> EnvironmentFieldOwnershipResponseStatus:
    if value in ENVIRONMENT_FIELD_OWNERSHIP_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_FIELD_OWNERSHIP_RESPONSE_STATUS_VALUES!r}"
    )
