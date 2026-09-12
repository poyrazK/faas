from typing import Literal

AdminStatusMaintenanceCreateRequestComponentsItem = Literal[
    "api_console", "app_execution", "deployments", "networking", "observability"
]

ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_COMPONENTS_ITEM_VALUES: set[
    AdminStatusMaintenanceCreateRequestComponentsItem
] = {
    "api_console",
    "app_execution",
    "deployments",
    "networking",
    "observability",
}


def check_admin_status_maintenance_create_request_components_item(
    value: str,
) -> AdminStatusMaintenanceCreateRequestComponentsItem:
    if value in ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_COMPONENTS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_COMPONENTS_ITEM_VALUES!r}"
    )
