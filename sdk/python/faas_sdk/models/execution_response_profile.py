from typing import Literal

ExecutionResponseProfile = Literal["python-data-v1", "standard"]

EXECUTION_RESPONSE_PROFILE_VALUES: set[ExecutionResponseProfile] = {
    "python-data-v1",
    "standard",
}


def check_execution_response_profile(value: str) -> ExecutionResponseProfile:
    if value in EXECUTION_RESPONSE_PROFILE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_RESPONSE_PROFILE_VALUES!r}")
