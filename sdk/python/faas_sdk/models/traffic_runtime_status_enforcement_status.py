from typing import Literal

TrafficRuntimeStatusEnforcementStatus = Literal["unverified"]

TRAFFIC_RUNTIME_STATUS_ENFORCEMENT_STATUS_VALUES: set[TrafficRuntimeStatusEnforcementStatus] = {
    "unverified",
}


def check_traffic_runtime_status_enforcement_status(value: str) -> TrafficRuntimeStatusEnforcementStatus:
    if value in TRAFFIC_RUNTIME_STATUS_ENFORCEMENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRAFFIC_RUNTIME_STATUS_ENFORCEMENT_STATUS_VALUES!r}")
