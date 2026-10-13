from typing import Literal

AppChangeTimelineResponseUnavailableSourcesItem = Literal[
    "activity", "deployment", "edge_rule", "health", "incident", "runtime_config"
]

APP_CHANGE_TIMELINE_RESPONSE_UNAVAILABLE_SOURCES_ITEM_VALUES: set[AppChangeTimelineResponseUnavailableSourcesItem] = {
    "activity",
    "deployment",
    "edge_rule",
    "health",
    "incident",
    "runtime_config",
}


def check_app_change_timeline_response_unavailable_sources_item(
    value: str,
) -> AppChangeTimelineResponseUnavailableSourcesItem:
    if value in APP_CHANGE_TIMELINE_RESPONSE_UNAVAILABLE_SOURCES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_CHANGE_TIMELINE_RESPONSE_UNAVAILABLE_SOURCES_ITEM_VALUES!r}"
    )
