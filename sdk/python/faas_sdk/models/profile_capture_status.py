from typing import Literal

ProfileCaptureStatus = Literal["capturing", "failed", "queued", "ready"]

PROFILE_CAPTURE_STATUS_VALUES: set[ProfileCaptureStatus] = {
    "capturing",
    "failed",
    "queued",
    "ready",
}


def check_profile_capture_status(value: str) -> ProfileCaptureStatus:
    if value in PROFILE_CAPTURE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_STATUS_VALUES!r}")
