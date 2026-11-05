from typing import Literal

RouteHealthGateOnRegression = Literal["abort", "hold"]

ROUTE_HEALTH_GATE_ON_REGRESSION_VALUES: set[RouteHealthGateOnRegression] = {
    "abort",
    "hold",
}


def check_route_health_gate_on_regression(value: str) -> RouteHealthGateOnRegression:
    if value in ROUTE_HEALTH_GATE_ON_REGRESSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_GATE_ON_REGRESSION_VALUES!r}")
