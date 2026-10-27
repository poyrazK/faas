from typing import Literal

OrgInvitationResponseStatus = Literal["consumed", "expired", "pending", "revoked"]

ORG_INVITATION_RESPONSE_STATUS_VALUES: set[OrgInvitationResponseStatus] = {
    "consumed",
    "expired",
    "pending",
    "revoked",
}


def check_org_invitation_response_status(value: str) -> OrgInvitationResponseStatus:
    if value in ORG_INVITATION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_INVITATION_RESPONSE_STATUS_VALUES!r}")
