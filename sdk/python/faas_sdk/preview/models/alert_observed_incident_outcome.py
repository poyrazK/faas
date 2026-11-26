from typing import Literal

AlertObservedIncidentOutcome = Literal["observation_ended", "recovered"]

ALERT_OBSERVED_INCIDENT_OUTCOME_VALUES: set[AlertObservedIncidentOutcome] = {
    "observation_ended",
    "recovered",
}


def check_alert_observed_incident_outcome(value: str) -> AlertObservedIncidentOutcome:
    if value in ALERT_OBSERVED_INCIDENT_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_OBSERVED_INCIDENT_OUTCOME_VALUES!r}")
