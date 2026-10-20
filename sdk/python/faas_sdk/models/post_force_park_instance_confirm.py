from typing import Literal

PostForceParkInstanceConfirm = Literal["true"]

POST_FORCE_PARK_INSTANCE_CONFIRM_VALUES: set[PostForceParkInstanceConfirm] = {
    "true",
}


def check_post_force_park_instance_confirm(value: str) -> PostForceParkInstanceConfirm:
    if value in POST_FORCE_PARK_INSTANCE_CONFIRM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {POST_FORCE_PARK_INSTANCE_CONFIRM_VALUES!r}")
