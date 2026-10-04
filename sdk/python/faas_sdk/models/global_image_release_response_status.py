from typing import Literal

GlobalImageReleaseResponseStatus = Literal["accepted", "blocked", "expired", "pending", "superseded"]

GLOBAL_IMAGE_RELEASE_RESPONSE_STATUS_VALUES: set[GlobalImageReleaseResponseStatus] = {
    "accepted",
    "blocked",
    "expired",
    "pending",
    "superseded",
}


def check_global_image_release_response_status(value: str) -> GlobalImageReleaseResponseStatus:
    if value in GLOBAL_IMAGE_RELEASE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GLOBAL_IMAGE_RELEASE_RESPONSE_STATUS_VALUES!r}")
