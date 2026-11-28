from typing import Literal

InsightEventSeriesUnit = Literal["customer", "subject"]

INSIGHT_EVENT_SERIES_UNIT_VALUES: set[InsightEventSeriesUnit] = {
    "customer",
    "subject",
}


def check_insight_event_series_unit(value: str) -> InsightEventSeriesUnit:
    if value in INSIGHT_EVENT_SERIES_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_EVENT_SERIES_UNIT_VALUES!r}")
