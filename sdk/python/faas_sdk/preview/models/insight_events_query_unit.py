from typing import Literal

InsightEventsQueryUnit = Literal["customer", "subject"]

INSIGHT_EVENTS_QUERY_UNIT_VALUES: set[InsightEventsQueryUnit] = {
    "customer",
    "subject",
}


def check_insight_events_query_unit(value: str) -> InsightEventsQueryUnit:
    if value in INSIGHT_EVENTS_QUERY_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INSIGHT_EVENTS_QUERY_UNIT_VALUES!r}")
