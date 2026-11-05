from typing import Literal

ImageRunResponseImageStatus = Literal["failed", "pending", "ready", "verifying_legacy"]

IMAGE_RUN_RESPONSE_IMAGE_STATUS_VALUES: set[ImageRunResponseImageStatus] = {
    "failed",
    "pending",
    "ready",
    "verifying_legacy",
}


def check_image_run_response_image_status(value: str) -> ImageRunResponseImageStatus:
    if value in IMAGE_RUN_RESPONSE_IMAGE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {IMAGE_RUN_RESPONSE_IMAGE_STATUS_VALUES!r}")
