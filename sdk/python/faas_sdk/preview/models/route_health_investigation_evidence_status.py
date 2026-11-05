from typing import Literal

RouteHealthInvestigationEvidenceStatus = Literal["observed", "unavailable"]

ROUTE_HEALTH_INVESTIGATION_EVIDENCE_STATUS_VALUES: set[RouteHealthInvestigationEvidenceStatus] = {
    "observed",
    "unavailable",
}


def check_route_health_investigation_evidence_status(value: str) -> RouteHealthInvestigationEvidenceStatus:
    if value in ROUTE_HEALTH_INVESTIGATION_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_INVESTIGATION_EVIDENCE_STATUS_VALUES!r}"
    )
