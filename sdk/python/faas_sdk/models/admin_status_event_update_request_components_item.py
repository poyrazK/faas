from typing import Literal

AdminStatusEventUpdateRequestComponentsItem = Literal[
    "api_console", "app_execution", "deployments", "networking", "observability"
]

ADMIN_STATUS_EVENT_UPDATE_REQUEST_COMPONENTS_ITEM_VALUES: set[AdminStatusEventUpdateRequestComponentsItem] = {
    "api_console",
    "app_execution",
    "deployments",
    "networking",
    "observability",
}


def check_admin_status_event_update_request_components_item(value: str) -> AdminStatusEventUpdateRequestComponentsItem:
    if value in ADMIN_STATUS_EVENT_UPDATE_REQUEST_COMPONENTS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_EVENT_UPDATE_REQUEST_COMPONENTS_ITEM_VALUES!r}"
    )
