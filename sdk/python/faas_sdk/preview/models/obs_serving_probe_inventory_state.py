from typing import Literal

ObsServingProbeInventoryState = Literal["healthy", "stale", "unavailable"]

OBS_SERVING_PROBE_INVENTORY_STATE_VALUES: set[ObsServingProbeInventoryState] = {
    "healthy",
    "stale",
    "unavailable",
}


def check_obs_serving_probe_inventory_state(value: str) -> ObsServingProbeInventoryState:
    if value in OBS_SERVING_PROBE_INVENTORY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_INVENTORY_STATE_VALUES!r}")
