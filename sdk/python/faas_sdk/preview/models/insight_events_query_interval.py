from typing import Literal

InsightEventsQueryInterval = Literal["day", "hour"]

INSIGHT_EVENTS_QUERY_INTERVAL_VALUES: set[InsightEventsQueryInterval] = {
    "day",
    "hour",
}


def check_insight_events_query_interval(value: str) -> InsightEventsQueryInterval:
    if value in INSIGHT_EVENTS_QUERY_INTERVAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_EVENTS_QUERY_INTERVAL_VALUES!r}")
