from typing import Literal

AppChangeEventSource = Literal["activity", "deployment", "edge_rule", "health", "incident", "runtime_config"]

APP_CHANGE_EVENT_SOURCE_VALUES: set[AppChangeEventSource] = {
    "activity",
    "deployment",
    "edge_rule",
    "health",
    "incident",
    "runtime_config",
}


def check_app_change_event_source(value: str) -> AppChangeEventSource:
    if value in APP_CHANGE_EVENT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_CHANGE_EVENT_SOURCE_VALUES!r}")
