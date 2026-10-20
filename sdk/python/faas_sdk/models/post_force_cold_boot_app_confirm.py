from typing import Literal

PostForceColdBootAppConfirm = Literal["true"]

POST_FORCE_COLD_BOOT_APP_CONFIRM_VALUES: set[PostForceColdBootAppConfirm] = {
    "true",
}


def check_post_force_cold_boot_app_confirm(value: str) -> PostForceColdBootAppConfirm:
    if value in POST_FORCE_COLD_BOOT_APP_CONFIRM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {POST_FORCE_COLD_BOOT_APP_CONFIRM_VALUES!r}")
