from typing import Literal

GetRouteHealthInvestigationSignal = Literal["errors", "latency"]

GET_ROUTE_HEALTH_INVESTIGATION_SIGNAL_VALUES: set[GetRouteHealthInvestigationSignal] = {
    "errors",
    "latency",
}


def check_get_route_health_investigation_signal(value: str) -> GetRouteHealthInvestigationSignal:
    if value in GET_ROUTE_HEALTH_INVESTIGATION_SIGNAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_ROUTE_HEALTH_INVESTIGATION_SIGNAL_VALUES!r}")
