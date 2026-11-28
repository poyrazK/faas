from typing import Literal

OrgResponseStatus = Literal["active", "deleted_pending", "past_due", "suspended"]

ORG_RESPONSE_STATUS_VALUES: set[OrgResponseStatus] = {
    "active",
    "deleted_pending",
    "past_due",
    "suspended",
}


def check_org_response_status(value: str) -> OrgResponseStatus:
    if value in ORG_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_RESPONSE_STATUS_VALUES!r}")
