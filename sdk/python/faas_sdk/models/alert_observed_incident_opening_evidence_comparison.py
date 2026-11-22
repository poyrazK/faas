from typing import Literal

AlertObservedIncidentOpeningEvidenceComparison = Literal["above_baseline", "below_baseline"]

ALERT_OBSERVED_INCIDENT_OPENING_EVIDENCE_COMPARISON_VALUES: set[AlertObservedIncidentOpeningEvidenceComparison] = {
    "above_baseline",
    "below_baseline",
}


def check_alert_observed_incident_opening_evidence_comparison(
    value: str,
) -> AlertObservedIncidentOpeningEvidenceComparison:
    if value in ALERT_OBSERVED_INCIDENT_OPENING_EVIDENCE_COMPARISON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ALERT_OBSERVED_INCIDENT_OPENING_EVIDENCE_COMPARISON_VALUES!r}"
    )
