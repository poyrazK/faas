from typing import Literal

ProfileCaptureDiffUnit = Literal["bytes", "count", "nanoseconds"]

PROFILE_CAPTURE_DIFF_UNIT_VALUES: set[ProfileCaptureDiffUnit] = {
    "bytes",
    "count",
    "nanoseconds",
}


def check_profile_capture_diff_unit(value: str) -> ProfileCaptureDiffUnit:
    if value in PROFILE_CAPTURE_DIFF_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_DIFF_UNIT_VALUES!r}")
