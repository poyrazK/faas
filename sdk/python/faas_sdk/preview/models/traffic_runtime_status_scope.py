from typing import Literal

TrafficRuntimeStatusScope = Literal["compute_gateway_wiring"]

TRAFFIC_RUNTIME_STATUS_SCOPE_VALUES: set[TrafficRuntimeStatusScope] = {
    "compute_gateway_wiring",
}


def check_traffic_runtime_status_scope(value: str) -> TrafficRuntimeStatusScope:
    if value in TRAFFIC_RUNTIME_STATUS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRAFFIC_RUNTIME_STATUS_SCOPE_VALUES!r}")
