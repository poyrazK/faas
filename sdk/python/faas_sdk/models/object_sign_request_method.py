from typing import Literal

ObjectSignRequestMethod = Literal["GET", "PUT"]

OBJECT_SIGN_REQUEST_METHOD_VALUES: set[ObjectSignRequestMethod] = {
    "GET",
    "PUT",
}


def check_object_sign_request_method(value: str) -> ObjectSignRequestMethod:
    if value in OBJECT_SIGN_REQUEST_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_SIGN_REQUEST_METHOD_VALUES!r}")
