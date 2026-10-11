from typing import Literal

ObjectSignRequestIfNoneMatch = Literal["*"]

OBJECT_SIGN_REQUEST_IF_NONE_MATCH_VALUES: set[ObjectSignRequestIfNoneMatch] = {
    "*",
}


def check_object_sign_request_if_none_match(value: str) -> ObjectSignRequestIfNoneMatch:
    if value in OBJECT_SIGN_REQUEST_IF_NONE_MATCH_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_SIGN_REQUEST_IF_NONE_MATCH_VALUES!r}")
