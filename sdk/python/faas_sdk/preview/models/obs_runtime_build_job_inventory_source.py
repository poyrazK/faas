from typing import Literal

ObsRuntimeBuildJobInventorySource = Literal["active_configured_compute_registry", "scrape_targets_minimum"]

OBS_RUNTIME_BUILD_JOB_INVENTORY_SOURCE_VALUES: set[ObsRuntimeBuildJobInventorySource] = {
    "active_configured_compute_registry",
    "scrape_targets_minimum",
}


def check_obs_runtime_build_job_inventory_source(value: str) -> ObsRuntimeBuildJobInventorySource:
    if value in OBS_RUNTIME_BUILD_JOB_INVENTORY_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_JOB_INVENTORY_SOURCE_VALUES!r}")
