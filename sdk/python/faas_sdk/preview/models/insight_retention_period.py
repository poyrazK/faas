from typing import Literal

InsightRetentionPeriod = Literal["day", "week"]

INSIGHT_RETENTION_PERIOD_VALUES: set[InsightRetentionPeriod] = {
    "day",
    "week",
}


def check_insight_retention_period(value: str) -> InsightRetentionPeriod:
    if value in INSIGHT_RETENTION_PERIOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_RETENTION_PERIOD_VALUES!r}")
