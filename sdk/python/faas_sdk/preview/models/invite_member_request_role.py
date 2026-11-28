from typing import Literal

InviteMemberRequestRole = Literal["admin", "billing", "developer", "viewer"]

INVITE_MEMBER_REQUEST_ROLE_VALUES: set[InviteMemberRequestRole] = {
    "admin",
    "billing",
    "developer",
    "viewer",
}


def check_invite_member_request_role(value: str) -> InviteMemberRequestRole:
    if value in INVITE_MEMBER_REQUEST_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVITE_MEMBER_REQUEST_ROLE_VALUES!r}")
