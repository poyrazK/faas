from typing import Literal

ExecutionProfileCapabilityProfile = Literal["python-data-v1", "standard"]

EXECUTION_PROFILE_CAPABILITY_PROFILE_VALUES: set[ExecutionProfileCapabilityProfile] = {
    "python-data-v1",
    "standard",
}


def check_execution_profile_capability_profile(value: str) -> ExecutionProfileCapabilityProfile:
    if value in EXECUTION_PROFILE_CAPABILITY_PROFILE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_PROFILE_CAPABILITY_PROFILE_VALUES!r}")
