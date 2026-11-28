from typing import Literal

GetProfileCaptureViewKind = Literal["block", "cpu", "heap", "mutex", "stacks"]

GET_PROFILE_CAPTURE_VIEW_KIND_VALUES: set[GetProfileCaptureViewKind] = {
    "block",
    "cpu",
    "heap",
    "mutex",
    "stacks",
}


def check_get_profile_capture_view_kind(value: str) -> GetProfileCaptureViewKind:
    if value in GET_PROFILE_CAPTURE_VIEW_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_PROFILE_CAPTURE_VIEW_KIND_VALUES!r}")
