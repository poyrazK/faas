from typing import Literal

RouteHealthWindowEvidenceErrorStatus = Literal["healthy", "regressed", "unknown"]

ROUTE_HEALTH_WINDOW_EVIDENCE_ERROR_STATUS_VALUES: set[RouteHealthWindowEvidenceErrorStatus] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_route_health_window_evidence_error_status(value: str) -> RouteHealthWindowEvidenceErrorStatus:
    if value in ROUTE_HEALTH_WINDOW_EVIDENCE_ERROR_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_WINDOW_EVIDENCE_ERROR_STATUS_VALUES!r}")
