from typing import Literal

GetProjectInsightEventsUnit = Literal["customer", "subject"]

GET_PROJECT_INSIGHT_EVENTS_UNIT_VALUES: set[GetProjectInsightEventsUnit] = {
    "customer",
    "subject",
}


def check_get_project_insight_events_unit(value: str) -> GetProjectInsightEventsUnit:
    if value in GET_PROJECT_INSIGHT_EVENTS_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_PROJECT_INSIGHT_EVENTS_UNIT_VALUES!r}")
