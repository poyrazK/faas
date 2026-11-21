from typing import Literal

GetProjectInsightEventsInterval = Literal["day", "hour"]

GET_PROJECT_INSIGHT_EVENTS_INTERVAL_VALUES: set[GetProjectInsightEventsInterval] = {
    "day",
    "hour",
}


def check_get_project_insight_events_interval(value: str) -> GetProjectInsightEventsInterval:
    if value in GET_PROJECT_INSIGHT_EVENTS_INTERVAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_PROJECT_INSIGHT_EVENTS_INTERVAL_VALUES!r}")
