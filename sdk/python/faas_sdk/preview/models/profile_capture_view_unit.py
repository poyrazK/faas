from typing import Literal

ProfileCaptureViewUnit = Literal["bytes", "count", "nanoseconds"]

PROFILE_CAPTURE_VIEW_UNIT_VALUES: set[ProfileCaptureViewUnit] = {
    "bytes",
    "count",
    "nanoseconds",
}


def check_profile_capture_view_unit(value: str) -> ProfileCaptureViewUnit:
    if value in PROFILE_CAPTURE_VIEW_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_VIEW_UNIT_VALUES!r}")
