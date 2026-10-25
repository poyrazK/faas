from typing import Literal

ProfileCaptureProfileKind = Literal["block", "cpu", "heap", "mutex", "stacks"]

PROFILE_CAPTURE_PROFILE_KIND_VALUES: set[ProfileCaptureProfileKind] = {
    "block",
    "cpu",
    "heap",
    "mutex",
    "stacks",
}


def check_profile_capture_profile_kind(value: str) -> ProfileCaptureProfileKind:
    if value in PROFILE_CAPTURE_PROFILE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_PROFILE_KIND_VALUES!r}")
