from typing import Literal

ProfileCaptureDiffKind = Literal["block", "cpu", "heap", "mutex", "stacks"]

PROFILE_CAPTURE_DIFF_KIND_VALUES: set[ProfileCaptureDiffKind] = {
    "block",
    "cpu",
    "heap",
    "mutex",
    "stacks",
}


def check_profile_capture_diff_kind(value: str) -> ProfileCaptureDiffKind:
    if value in PROFILE_CAPTURE_DIFF_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_DIFF_KIND_VALUES!r}")
