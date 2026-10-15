from typing import Literal

ProfileCaptureTrigger = Literal["cpu_saturation", "manual", "memory_pressure", "slow_request"]

PROFILE_CAPTURE_TRIGGER_VALUES: set[ProfileCaptureTrigger] = {
    "cpu_saturation",
    "manual",
    "memory_pressure",
    "slow_request",
}


def check_profile_capture_trigger(value: str) -> ProfileCaptureTrigger:
    if value in PROFILE_CAPTURE_TRIGGER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_CAPTURE_TRIGGER_VALUES!r}")
