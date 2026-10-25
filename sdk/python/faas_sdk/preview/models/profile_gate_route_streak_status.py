from typing import Literal

ProfileGateRouteStreakStatus = Literal["insufficient_data", "no_regression_detected", "regressed"]

PROFILE_GATE_ROUTE_STREAK_STATUS_VALUES: set[ProfileGateRouteStreakStatus] = {
    "insufficient_data",
    "no_regression_detected",
    "regressed",
}


def check_profile_gate_route_streak_status(value: str) -> ProfileGateRouteStreakStatus:
    if value in PROFILE_GATE_ROUTE_STREAK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_GATE_ROUTE_STREAK_STATUS_VALUES!r}")
