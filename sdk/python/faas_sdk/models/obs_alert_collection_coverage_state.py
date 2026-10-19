from typing import Literal

ObsAlertCollectionCoverageState = Literal["observed", "partial", "unknown"]

OBS_ALERT_COLLECTION_COVERAGE_STATE_VALUES: set[ObsAlertCollectionCoverageState] = {
    "observed",
    "partial",
    "unknown",
}


def check_obs_alert_collection_coverage_state(value: str) -> ObsAlertCollectionCoverageState:
    if value in OBS_ALERT_COLLECTION_COVERAGE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_COVERAGE_STATE_VALUES!r}")
