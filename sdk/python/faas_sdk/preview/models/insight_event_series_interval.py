from typing import Literal

InsightEventSeriesInterval = Literal["day", "hour"]

INSIGHT_EVENT_SERIES_INTERVAL_VALUES: set[InsightEventSeriesInterval] = {
    "day",
    "hour",
}


def check_insight_event_series_interval(value: str) -> InsightEventSeriesInterval:
    if value in INSIGHT_EVENT_SERIES_INTERVAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_EVENT_SERIES_INTERVAL_VALUES!r}")
