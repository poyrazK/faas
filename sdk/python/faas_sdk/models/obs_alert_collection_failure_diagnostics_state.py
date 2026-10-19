from typing import Literal

ObsAlertCollectionFailureDiagnosticsState = Literal["recorded", "unknown"]

OBS_ALERT_COLLECTION_FAILURE_DIAGNOSTICS_STATE_VALUES: set[ObsAlertCollectionFailureDiagnosticsState] = {
    "recorded",
    "unknown",
}


def check_obs_alert_collection_failure_diagnostics_state(value: str) -> ObsAlertCollectionFailureDiagnosticsState:
    if value in OBS_ALERT_COLLECTION_FAILURE_DIAGNOSTICS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_FAILURE_DIAGNOSTICS_STATE_VALUES!r}"
    )
