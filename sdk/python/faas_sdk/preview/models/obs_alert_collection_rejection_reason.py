from typing import Literal

ObsAlertCollectionRejectionReason = Literal[
    "collector_validation_failed", "no_alert_rules_observed", "rule_diagnostics_present"
]

OBS_ALERT_COLLECTION_REJECTION_REASON_VALUES: set[ObsAlertCollectionRejectionReason] = {
    "collector_validation_failed",
    "no_alert_rules_observed",
    "rule_diagnostics_present",
}


def check_obs_alert_collection_rejection_reason(value: str) -> ObsAlertCollectionRejectionReason:
    if value in OBS_ALERT_COLLECTION_REJECTION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_COLLECTION_REJECTION_REASON_VALUES!r}")
