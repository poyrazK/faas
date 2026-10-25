from typing import Literal

CreateImageRunRequestPull = Literal["always", "missing"]

CREATE_IMAGE_RUN_REQUEST_PULL_VALUES: set[CreateImageRunRequestPull] = {
    "always",
    "missing",
}


def check_create_image_run_request_pull(value: str) -> CreateImageRunRequestPull:
    if value in CREATE_IMAGE_RUN_REQUEST_PULL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_IMAGE_RUN_REQUEST_PULL_VALUES!r}")
