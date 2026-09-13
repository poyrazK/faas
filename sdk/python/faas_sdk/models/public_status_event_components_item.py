from typing import Literal

PublicStatusEventComponentsItem = Literal["api_console", "app_execution", "deployments", "networking", "observability"]

PUBLIC_STATUS_EVENT_COMPONENTS_ITEM_VALUES: set[PublicStatusEventComponentsItem] = {
    "api_console",
    "app_execution",
    "deployments",
    "networking",
    "observability",
}


def check_public_status_event_components_item(value: str) -> PublicStatusEventComponentsItem:
    if value in PUBLIC_STATUS_EVENT_COMPONENTS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_EVENT_COMPONENTS_ITEM_VALUES!r}")
