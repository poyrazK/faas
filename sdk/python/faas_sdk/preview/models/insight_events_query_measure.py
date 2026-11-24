from typing import Literal

InsightEventsQueryMeasure = Literal["events", "value"]

INSIGHT_EVENTS_QUERY_MEASURE_VALUES: set[InsightEventsQueryMeasure] = {
    "events",
    "value",
}


def check_insight_events_query_measure(value: str) -> InsightEventsQueryMeasure:
    if value in INSIGHT_EVENTS_QUERY_MEASURE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_EVENTS_QUERY_MEASURE_VALUES!r}")
