from typing import Literal

InsightRetentionRequestPeriod = Literal["day", "week"]

INSIGHT_RETENTION_REQUEST_PERIOD_VALUES: set[InsightRetentionRequestPeriod] = {
    "day",
    "week",
}


def check_insight_retention_request_period(value: str) -> InsightRetentionRequestPeriod:
    if value in INSIGHT_RETENTION_REQUEST_PERIOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_RETENTION_REQUEST_PERIOD_VALUES!r}")
