from typing import Literal

DataEndpointCapacityState = Literal["insufficient_capacity", "needs_qualification", "ready"]

DATA_ENDPOINT_CAPACITY_STATE_VALUES: set[DataEndpointCapacityState] = {
    "insufficient_capacity",
    "needs_qualification",
    "ready",
}


def check_data_endpoint_capacity_state(value: str) -> DataEndpointCapacityState:
    if value in DATA_ENDPOINT_CAPACITY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_ENDPOINT_CAPACITY_STATE_VALUES!r}")
