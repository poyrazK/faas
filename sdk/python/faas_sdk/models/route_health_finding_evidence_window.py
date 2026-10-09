from typing import Literal

RouteHealthFindingEvidenceWindow = Literal["pooled"]

ROUTE_HEALTH_FINDING_EVIDENCE_WINDOW_VALUES: set[RouteHealthFindingEvidenceWindow] = {
    "pooled",
}


def check_route_health_finding_evidence_window(value: str) -> RouteHealthFindingEvidenceWindow:
    if value in ROUTE_HEALTH_FINDING_EVIDENCE_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_FINDING_EVIDENCE_WINDOW_VALUES!r}")
