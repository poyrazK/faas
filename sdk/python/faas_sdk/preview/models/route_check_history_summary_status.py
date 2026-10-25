from typing import Literal

RouteCheckHistorySummaryStatus = Literal["satisfied", "unknown", "violated"]

ROUTE_CHECK_HISTORY_SUMMARY_STATUS_VALUES: set[RouteCheckHistorySummaryStatus] = {
    "satisfied",
    "unknown",
    "violated",
}


def check_route_check_history_summary_status(value: str) -> RouteCheckHistorySummaryStatus:
    if value in ROUTE_CHECK_HISTORY_SUMMARY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CHECK_HISTORY_SUMMARY_STATUS_VALUES!r}")
