from typing import Literal

ProfileCaptureDiffKind = Literal["cpu", "heap", "stacks"]

PROFILE_CAPTURE_DIFF_KIND_VALUES: set[ProfileCaptureDiffKind] = {
    "cpu",
    "heap",
    "stacks",
}


def check_profile_capture_diff_kind(value: str) -> ProfileCaptureDiffKind:
    if value in PROFILE_CAPTURE_DIFF_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_DIFF_KIND_VALUES!r}")
