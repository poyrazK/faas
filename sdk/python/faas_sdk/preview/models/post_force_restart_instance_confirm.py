from typing import Literal

PostForceRestartInstanceConfirm = Literal["true"]

POST_FORCE_RESTART_INSTANCE_CONFIRM_VALUES: set[PostForceRestartInstanceConfirm] = {
    "true",
}


def check_post_force_restart_instance_confirm(value: str) -> PostForceRestartInstanceConfirm:
    if value in POST_FORCE_RESTART_INSTANCE_CONFIRM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {POST_FORCE_RESTART_INSTANCE_CONFIRM_VALUES!r}")
