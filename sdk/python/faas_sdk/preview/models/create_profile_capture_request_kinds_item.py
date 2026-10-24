from typing import Literal

CreateProfileCaptureRequestKindsItem = Literal["block", "cpu", "heap", "mutex", "stacks"]

CREATE_PROFILE_CAPTURE_REQUEST_KINDS_ITEM_VALUES: set[CreateProfileCaptureRequestKindsItem] = {
    "block",
    "cpu",
    "heap",
    "mutex",
    "stacks",
}


def check_create_profile_capture_request_kinds_item(value: str) -> CreateProfileCaptureRequestKindsItem:
    if value in CREATE_PROFILE_CAPTURE_REQUEST_KINDS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_PROFILE_CAPTURE_REQUEST_KINDS_ITEM_VALUES!r}")
