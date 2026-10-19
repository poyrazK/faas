from typing import Literal

ProfileCaptureKindsItem = Literal["block", "cpu", "heap", "mutex", "stacks"]

PROFILE_CAPTURE_KINDS_ITEM_VALUES: set[ProfileCaptureKindsItem] = {
    "block",
    "cpu",
    "heap",
    "mutex",
    "stacks",
}


def check_profile_capture_kinds_item(value: str) -> ProfileCaptureKindsItem:
    if value in PROFILE_CAPTURE_KINDS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_KINDS_ITEM_VALUES!r}")
