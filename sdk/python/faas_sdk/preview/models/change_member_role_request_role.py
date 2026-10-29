from typing import Literal

ChangeMemberRoleRequestRole = Literal["admin", "billing", "developer", "viewer"]

CHANGE_MEMBER_ROLE_REQUEST_ROLE_VALUES: set[ChangeMemberRoleRequestRole] = {
    "admin",
    "billing",
    "developer",
    "viewer",
}


def check_change_member_role_request_role(value: str) -> ChangeMemberRoleRequestRole:
    if value in CHANGE_MEMBER_ROLE_REQUEST_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CHANGE_MEMBER_ROLE_REQUEST_ROLE_VALUES!r}")
