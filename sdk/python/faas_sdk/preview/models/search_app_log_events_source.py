from typing import Literal

SearchAppLogEventsSource = Literal["build", "deploy", "dns", "http", "network", "runtime"]

SEARCH_APP_LOG_EVENTS_SOURCE_VALUES: set[SearchAppLogEventsSource] = {
    "build",
    "deploy",
    "dns",
    "http",
    "network",
    "runtime",
}


def check_search_app_log_events_source(value: str) -> SearchAppLogEventsSource:
    if value in SEARCH_APP_LOG_EVENTS_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SEARCH_APP_LOG_EVENTS_SOURCE_VALUES!r}")
