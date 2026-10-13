from typing import Literal

DailyUsageResponseKind = Literal["app", "job", "run"]

DAILY_USAGE_RESPONSE_KIND_VALUES: set[DailyUsageResponseKind] = {
    "app",
    "job",
    "run",
}


def check_daily_usage_response_kind(value: str) -> DailyUsageResponseKind:
    if value in DAILY_USAGE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DAILY_USAGE_RESPONSE_KIND_VALUES!r}")
