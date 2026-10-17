from typing import Literal

TrafficRuntimeStatusState = Literal["observed", "partial", "unverified"]

TRAFFIC_RUNTIME_STATUS_STATE_VALUES: set[TrafficRuntimeStatusState] = {
    "observed",
    "partial",
    "unverified",
}


def check_traffic_runtime_status_state(value: str) -> TrafficRuntimeStatusState:
    if value in TRAFFIC_RUNTIME_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRAFFIC_RUNTIME_STATUS_STATE_VALUES!r}")
