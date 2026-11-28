from typing import Literal

AppBindingInventoryServiceBindingSource = Literal["app", "environment_release", "unavailable"]

APP_BINDING_INVENTORY_SERVICE_BINDING_SOURCE_VALUES: set[AppBindingInventoryServiceBindingSource] = {
    "app",
    "environment_release",
    "unavailable",
}


def check_app_binding_inventory_service_binding_source(value: str) -> AppBindingInventoryServiceBindingSource:
    if value in APP_BINDING_INVENTORY_SERVICE_BINDING_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_BINDING_INVENTORY_SERVICE_BINDING_SOURCE_VALUES!r}"
    )
