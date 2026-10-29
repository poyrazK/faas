from typing import Literal

BuildResponseFailureClass = Literal["infra", "oom", "timeout", "user_error"]

BUILD_RESPONSE_FAILURE_CLASS_VALUES: set[BuildResponseFailureClass] = {
    "infra",
    "oom",
    "timeout",
    "user_error",
}


def check_build_response_failure_class(value: str) -> BuildResponseFailureClass:
    if value in BUILD_RESPONSE_FAILURE_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BUILD_RESPONSE_FAILURE_CLASS_VALUES!r}")
