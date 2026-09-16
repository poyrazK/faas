from typing import Literal

PublicStatusUpdateComponentsItem = Literal["api_console", "app_execution", "deployments", "networking", "observability"]

PUBLIC_STATUS_UPDATE_COMPONENTS_ITEM_VALUES: set[PublicStatusUpdateComponentsItem] = {
    "api_console",
    "app_execution",
    "deployments",
    "networking",
    "observability",
}


def check_public_status_update_components_item(value: str) -> PublicStatusUpdateComponentsItem:
    if value in PUBLIC_STATUS_UPDATE_COMPONENTS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_UPDATE_COMPONENTS_ITEM_VALUES!r}")
