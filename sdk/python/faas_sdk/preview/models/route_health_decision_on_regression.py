from typing import Literal

RouteHealthDecisionOnRegression = Literal["abort", "hold"]

ROUTE_HEALTH_DECISION_ON_REGRESSION_VALUES: set[RouteHealthDecisionOnRegression] = {
    "abort",
    "hold",
}


def check_route_health_decision_on_regression(value: str) -> RouteHealthDecisionOnRegression:
    if value in ROUTE_HEALTH_DECISION_ON_REGRESSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_DECISION_ON_REGRESSION_VALUES!r}")
