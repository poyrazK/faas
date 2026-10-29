from typing import Literal

GetAppLogEventFacetsSource = Literal["build", "deploy", "dns", "http", "network", "runtime"]

GET_APP_LOG_EVENT_FACETS_SOURCE_VALUES: set[GetAppLogEventFacetsSource] = {
    "build",
    "deploy",
    "dns",
    "http",
    "network",
    "runtime",
}


def check_get_app_log_event_facets_source(value: str) -> GetAppLogEventFacetsSource:
    if value in GET_APP_LOG_EVENT_FACETS_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_LOG_EVENT_FACETS_SOURCE_VALUES!r}")
