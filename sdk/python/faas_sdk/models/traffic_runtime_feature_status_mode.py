from typing import Literal

TrafficRuntimeFeatureStatusMode = Literal[
    "central", "disabled", "enabled", "local", "mixed", "redis", "shared", "unwired"
]

TRAFFIC_RUNTIME_FEATURE_STATUS_MODE_VALUES: set[TrafficRuntimeFeatureStatusMode] = {
    "central",
    "disabled",
    "enabled",
    "local",
    "mixed",
    "redis",
    "shared",
    "unwired",
}


def check_traffic_runtime_feature_status_mode(value: str) -> TrafficRuntimeFeatureStatusMode:
    if value in TRAFFIC_RUNTIME_FEATURE_STATUS_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRAFFIC_RUNTIME_FEATURE_STATUS_MODE_VALUES!r}")
