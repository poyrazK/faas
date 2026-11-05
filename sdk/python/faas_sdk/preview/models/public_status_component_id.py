from typing import Literal

PublicStatusComponentId = Literal["api_console", "app_execution", "deployments", "networking", "observability"]

PUBLIC_STATUS_COMPONENT_ID_VALUES: set[PublicStatusComponentId] = {
    "api_console",
    "app_execution",
    "deployments",
    "networking",
    "observability",
}


def check_public_status_component_id(value: str) -> PublicStatusComponentId:
    if value in PUBLIC_STATUS_COMPONENT_ID_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_COMPONENT_ID_VALUES!r}")
