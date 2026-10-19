from typing import Literal

ObsAlertCollectionRejectionScope = Literal["rejected_snapshot"]

OBS_ALERT_COLLECTION_REJECTION_SCOPE_VALUES: set[ObsAlertCollectionRejectionScope] = {
    "rejected_snapshot",
}


def check_obs_alert_collection_rejection_scope(value: str) -> ObsAlertCollectionRejectionScope:
    if value in OBS_ALERT_COLLECTION_REJECTION_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_REJECTION_SCOPE_VALUES!r}")
