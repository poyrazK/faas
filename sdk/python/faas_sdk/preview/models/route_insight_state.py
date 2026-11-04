from typing import Literal

RouteInsightState = Literal["active", "resolved"]

ROUTE_INSIGHT_STATE_VALUES: set[RouteInsightState] = {
    "active",
    "resolved",
}


def check_route_insight_state(value: str) -> RouteInsightState:
    if value in ROUTE_INSIGHT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_INSIGHT_STATE_VALUES!r}")
