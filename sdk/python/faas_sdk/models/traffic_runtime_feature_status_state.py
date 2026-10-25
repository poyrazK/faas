from typing import Literal

TrafficRuntimeFeatureStatusState = Literal["mixed", "observed", "unverified"]

TRAFFIC_RUNTIME_FEATURE_STATUS_STATE_VALUES: set[TrafficRuntimeFeatureStatusState] = {
    "mixed",
    "observed",
    "unverified",
}


def check_traffic_runtime_feature_status_state(value: str) -> TrafficRuntimeFeatureStatusState:
    if value in TRAFFIC_RUNTIME_FEATURE_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRAFFIC_RUNTIME_FEATURE_STATUS_STATE_VALUES!r}")
