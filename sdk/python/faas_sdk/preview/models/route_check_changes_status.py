from typing import Literal

RouteCheckChangesStatus = Literal[
    "ambiguous", "comparable", "initial", "requirements_changed", "tracking_limit", "unavailable"
]

ROUTE_CHECK_CHANGES_STATUS_VALUES: set[RouteCheckChangesStatus] = {
    "ambiguous",
    "comparable",
    "initial",
    "requirements_changed",
    "tracking_limit",
    "unavailable",
}


def check_route_check_changes_status(value: str) -> RouteCheckChangesStatus:
    if value in ROUTE_CHECK_CHANGES_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CHECK_CHANGES_STATUS_VALUES!r}")
