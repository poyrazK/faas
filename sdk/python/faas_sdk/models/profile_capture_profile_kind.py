from typing import Literal

ProfileCaptureProfileKind = Literal["cpu", "heap", "stacks"]

PROFILE_CAPTURE_PROFILE_KIND_VALUES: set[ProfileCaptureProfileKind] = {
    "cpu",
    "heap",
    "stacks",
}


def check_profile_capture_profile_kind(value: str) -> ProfileCaptureProfileKind:
    if value in PROFILE_CAPTURE_PROFILE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_PROFILE_KIND_VALUES!r}")
