from typing import Literal

OrgMemberResponseRole = Literal["admin", "billing", "developer", "owner", "viewer"]

ORG_MEMBER_RESPONSE_ROLE_VALUES: set[OrgMemberResponseRole] = {
    "admin",
    "billing",
    "developer",
    "owner",
    "viewer",
}


def check_org_member_response_role(value: str) -> OrgMemberResponseRole:
    if value in ORG_MEMBER_RESPONSE_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_MEMBER_RESPONSE_ROLE_VALUES!r}")
