from typing import Literal

ObjectSignedRequestMethod = Literal["GET", "PUT"]

OBJECT_SIGNED_REQUEST_METHOD_VALUES: set[ObjectSignedRequestMethod] = {
    "GET",
    "PUT",
}


def check_object_signed_request_method(value: str) -> ObjectSignedRequestMethod:
    if value in OBJECT_SIGNED_REQUEST_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_SIGNED_REQUEST_METHOD_VALUES!r}")
