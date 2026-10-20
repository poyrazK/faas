from typing import Literal

EnvironmentFieldOwnershipRequestManager = Literal["terraform"]

ENVIRONMENT_FIELD_OWNERSHIP_REQUEST_MANAGER_VALUES: set[EnvironmentFieldOwnershipRequestManager] = {
    "terraform",
}


def check_environment_field_ownership_request_manager(value: str) -> EnvironmentFieldOwnershipRequestManager:
    if value in ENVIRONMENT_FIELD_OWNERSHIP_REQUEST_MANAGER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_FIELD_OWNERSHIP_REQUEST_MANAGER_VALUES!r}"
    )
