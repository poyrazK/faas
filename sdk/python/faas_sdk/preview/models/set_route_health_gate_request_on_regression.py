from typing import Literal

SetRouteHealthGateRequestOnRegression = Literal["abort", "hold"]

SET_ROUTE_HEALTH_GATE_REQUEST_ON_REGRESSION_VALUES: set[SetRouteHealthGateRequestOnRegression] = {
    "abort",
    "hold",
}


def check_set_route_health_gate_request_on_regression(value: str) -> SetRouteHealthGateRequestOnRegression:
    if value in SET_ROUTE_HEALTH_GATE_REQUEST_ON_REGRESSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_ROUTE_HEALTH_GATE_REQUEST_ON_REGRESSION_VALUES!r}"
    )
