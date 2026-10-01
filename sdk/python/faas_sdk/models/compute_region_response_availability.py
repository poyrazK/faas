from typing import Literal

ComputeRegionResponseAvailability = Literal["available", "unavailable"]

COMPUTE_REGION_RESPONSE_AVAILABILITY_VALUES: set[ComputeRegionResponseAvailability] = {
    "available",
    "unavailable",
}


def check_compute_region_response_availability(value: str) -> ComputeRegionResponseAvailability:
    if value in COMPUTE_REGION_RESPONSE_AVAILABILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMPUTE_REGION_RESPONSE_AVAILABILITY_VALUES!r}")
