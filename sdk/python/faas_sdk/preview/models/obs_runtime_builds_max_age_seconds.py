from typing import Literal

ObsRuntimeBuildsMaxAgeSeconds = Literal[120]

OBS_RUNTIME_BUILDS_MAX_AGE_SECONDS_VALUES: set[ObsRuntimeBuildsMaxAgeSeconds] = {
    120,
}


def check_obs_runtime_builds_max_age_seconds(value: int) -> ObsRuntimeBuildsMaxAgeSeconds:
    if value in OBS_RUNTIME_BUILDS_MAX_AGE_SECONDS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDS_MAX_AGE_SECONDS_VALUES!r}")
