from typing import Literal

SyntheticCheckResponseMethod = Literal["GET", "HEAD"]

SYNTHETIC_CHECK_RESPONSE_METHOD_VALUES: set[SyntheticCheckResponseMethod] = {
    "GET",
    "HEAD",
}


def check_synthetic_check_response_method(value: str) -> SyntheticCheckResponseMethod:
    if value in SYNTHETIC_CHECK_RESPONSE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SYNTHETIC_CHECK_RESPONSE_METHOD_VALUES!r}")
