from typing import Literal

RouteLifecycleHistoryApprovalStatus = Literal["expired", "invalidated", "unavailable", "valid"]

ROUTE_LIFECYCLE_HISTORY_APPROVAL_STATUS_VALUES: set[RouteLifecycleHistoryApprovalStatus] = {
    "expired",
    "invalidated",
    "unavailable",
    "valid",
}


def check_route_lifecycle_history_approval_status(value: str) -> RouteLifecycleHistoryApprovalStatus:
    if value in ROUTE_LIFECYCLE_HISTORY_APPROVAL_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_LIFECYCLE_HISTORY_APPROVAL_STATUS_VALUES!r}")
