from typing import Literal

OrgWithRoleRole = Literal["admin", "billing", "developer", "owner", "viewer"]

ORG_WITH_ROLE_ROLE_VALUES: set[OrgWithRoleRole] = {
    "admin",
    "billing",
    "developer",
    "owner",
    "viewer",
}


def check_org_with_role_role(value: str) -> OrgWithRoleRole:
    if value in ORG_WITH_ROLE_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_WITH_ROLE_ROLE_VALUES!r}")
