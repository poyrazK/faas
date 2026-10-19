from typing import Literal

ObsAlertCollectionCoverageScope = Literal["recorded_collection_cadence"]

OBS_ALERT_COLLECTION_COVERAGE_SCOPE_VALUES: set[ObsAlertCollectionCoverageScope] = {
    "recorded_collection_cadence",
}


def check_obs_alert_collection_coverage_scope(value: str) -> ObsAlertCollectionCoverageScope:
    if value in OBS_ALERT_COLLECTION_COVERAGE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_COVERAGE_SCOPE_VALUES!r}")
