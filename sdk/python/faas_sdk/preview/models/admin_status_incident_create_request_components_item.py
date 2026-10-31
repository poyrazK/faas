from typing import Literal

AdminStatusIncidentCreateRequestComponentsItem = Literal[
    "api_console", "app_execution", "deployments", "networking", "observability"
]

ADMIN_STATUS_INCIDENT_CREATE_REQUEST_COMPONENTS_ITEM_VALUES: set[AdminStatusIncidentCreateRequestComponentsItem] = {
    "api_console",
    "app_execution",
    "deployments",
    "networking",
    "observability",
}


def check_admin_status_incident_create_request_components_item(
    value: str,
) -> AdminStatusIncidentCreateRequestComponentsItem:
    if value in ADMIN_STATUS_INCIDENT_CREATE_REQUEST_COMPONENTS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_INCIDENT_CREATE_REQUEST_COMPONENTS_ITEM_VALUES!r}"
    )
