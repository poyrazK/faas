from typing import Literal

DevPatchStatusResponseState = Literal["applied", "failed", "pending"]

DEV_PATCH_STATUS_RESPONSE_STATE_VALUES: set[DevPatchStatusResponseState] = {
    "applied",
    "failed",
    "pending",
}


def check_dev_patch_status_response_state(value: str) -> DevPatchStatusResponseState:
    if value in DEV_PATCH_STATUS_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_PATCH_STATUS_RESPONSE_STATE_VALUES!r}")
