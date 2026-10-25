from typing import Literal

ProfileCaptureViewKind = Literal["block", "cpu", "heap", "mutex", "stacks"]

PROFILE_CAPTURE_VIEW_KIND_VALUES: set[ProfileCaptureViewKind] = {
    "block",
    "cpu",
    "heap",
    "mutex",
    "stacks",
}


def check_profile_capture_view_kind(value: str) -> ProfileCaptureViewKind:
    if value in PROFILE_CAPTURE_VIEW_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_VIEW_KIND_VALUES!r}")
