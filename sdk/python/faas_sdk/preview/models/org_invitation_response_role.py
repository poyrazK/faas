from typing import Literal

OrgInvitationResponseRole = Literal["admin", "billing", "developer", "viewer"]

ORG_INVITATION_RESPONSE_ROLE_VALUES: set[OrgInvitationResponseRole] = {
    "admin",
    "billing",
    "developer",
    "viewer",
}


def check_org_invitation_response_role(value: str) -> OrgInvitationResponseRole:
    if value in ORG_INVITATION_RESPONSE_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_INVITATION_RESPONSE_ROLE_VALUES!r}")
