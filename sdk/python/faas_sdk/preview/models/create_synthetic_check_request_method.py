from typing import Literal

CreateSyntheticCheckRequestMethod = Literal["GET", "HEAD"]

CREATE_SYNTHETIC_CHECK_REQUEST_METHOD_VALUES: set[CreateSyntheticCheckRequestMethod] = {
    "GET",
    "HEAD",
}


def check_create_synthetic_check_request_method(value: str) -> CreateSyntheticCheckRequestMethod:
    if value in CREATE_SYNTHETIC_CHECK_REQUEST_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_SYNTHETIC_CHECK_REQUEST_METHOD_VALUES!r}")
