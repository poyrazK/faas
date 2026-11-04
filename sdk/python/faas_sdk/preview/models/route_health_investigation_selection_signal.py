from typing import Literal

RouteHealthInvestigationSelectionSignal = Literal["latency"]

ROUTE_HEALTH_INVESTIGATION_SELECTION_SIGNAL_VALUES: set[RouteHealthInvestigationSelectionSignal] = {
    "latency",
}


def check_route_health_investigation_selection_signal(value: str) -> RouteHealthInvestigationSelectionSignal:
    if value in ROUTE_HEALTH_INVESTIGATION_SELECTION_SIGNAL_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_INVESTIGATION_SELECTION_SIGNAL_VALUES!r}"
    )
