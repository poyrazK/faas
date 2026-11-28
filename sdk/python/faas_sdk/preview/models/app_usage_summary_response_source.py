from typing import Literal

AppUsageSummaryResponseSource = Literal["mixed", "usage_daily", "usage_minutes"]

APP_USAGE_SUMMARY_RESPONSE_SOURCE_VALUES: set[AppUsageSummaryResponseSource] = {
    "mixed",
    "usage_daily",
    "usage_minutes",
}


def check_app_usage_summary_response_source(value: str) -> AppUsageSummaryResponseSource:
    if value in APP_USAGE_SUMMARY_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_USAGE_SUMMARY_RESPONSE_SOURCE_VALUES!r}")
