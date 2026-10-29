from typing import Literal

CrashCaptureResponseStatus = Literal["capturing", "expired", "failed", "ready", "requested"]

CRASH_CAPTURE_RESPONSE_STATUS_VALUES: set[CrashCaptureResponseStatus] = {
    "capturing",
    "expired",
    "failed",
    "ready",
    "requested",
}


def check_crash_capture_response_status(value: str) -> CrashCaptureResponseStatus:
    if value in CRASH_CAPTURE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CRASH_CAPTURE_RESPONSE_STATUS_VALUES!r}")
