from typing import Literal

OrgMembershipExportResponseRole = Literal["admin", "billing", "developer", "owner", "viewer"]

ORG_MEMBERSHIP_EXPORT_RESPONSE_ROLE_VALUES: set[OrgMembershipExportResponseRole] = {
    "admin",
    "billing",
    "developer",
    "owner",
    "viewer",
}


def check_org_membership_export_response_role(value: str) -> OrgMembershipExportResponseRole:
    if value in ORG_MEMBERSHIP_EXPORT_RESPONSE_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_MEMBERSHIP_EXPORT_RESPONSE_ROLE_VALUES!r}")
