from typing import Literal

RouteCheckHistorySummaryComparisonStatus = Literal[
    "ambiguous", "comparable", "initial", "requirements_changed", "tracking_limit", "unavailable"
]

ROUTE_CHECK_HISTORY_SUMMARY_COMPARISON_STATUS_VALUES: set[RouteCheckHistorySummaryComparisonStatus] = {
    "ambiguous",
    "comparable",
    "initial",
    "requirements_changed",
    "tracking_limit",
    "unavailable",
}


def check_route_check_history_summary_comparison_status(value: str) -> RouteCheckHistorySummaryComparisonStatus:
    if value in ROUTE_CHECK_HISTORY_SUMMARY_COMPARISON_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_CHECK_HISTORY_SUMMARY_COMPARISON_STATUS_VALUES!r}"
    )
