from typing import Literal

ObsRuntimeBuilderHealthState = Literal["incomplete", "observed", "overdue", "unknown"]

OBS_RUNTIME_BUILDER_HEALTH_STATE_VALUES: set[ObsRuntimeBuilderHealthState] = {
    "incomplete",
    "observed",
    "overdue",
    "unknown",
}


def check_obs_runtime_builder_health_state(value: str) -> ObsRuntimeBuilderHealthState:
    if value in OBS_RUNTIME_BUILDER_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDER_HEALTH_STATE_VALUES!r}")
