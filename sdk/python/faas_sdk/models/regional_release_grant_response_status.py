from typing import Literal

RegionalReleaseGrantResponseStatus = Literal["active", "expired", "revoked"]

REGIONAL_RELEASE_GRANT_RESPONSE_STATUS_VALUES: set[RegionalReleaseGrantResponseStatus] = {
    "active",
    "expired",
    "revoked",
}


def check_regional_release_grant_response_status(value: str) -> RegionalReleaseGrantResponseStatus:
    if value in REGIONAL_RELEASE_GRANT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REGIONAL_RELEASE_GRANT_RESPONSE_STATUS_VALUES!r}")
