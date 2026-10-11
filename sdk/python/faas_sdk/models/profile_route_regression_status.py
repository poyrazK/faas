from typing import Literal

ProfileRouteRegressionStatus = Literal["insufficient_data", "no_regression_detected", "regressed"]

PROFILE_ROUTE_REGRESSION_STATUS_VALUES: set[ProfileRouteRegressionStatus] = {
    "insufficient_data",
    "no_regression_detected",
    "regressed",
}


def check_profile_route_regression_status(value: str) -> ProfileRouteRegressionStatus:
    if value in PROFILE_ROUTE_REGRESSION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_ROUTE_REGRESSION_STATUS_VALUES!r}")
