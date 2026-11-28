from typing import Literal

ImageRunResponseStatus = Literal["pulling", "queued"]

IMAGE_RUN_RESPONSE_STATUS_VALUES: set[ImageRunResponseStatus] = {
    "pulling",
    "queued",
}


def check_image_run_response_status(value: str) -> ImageRunResponseStatus:
    if value in IMAGE_RUN_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {IMAGE_RUN_RESPONSE_STATUS_VALUES!r}")
