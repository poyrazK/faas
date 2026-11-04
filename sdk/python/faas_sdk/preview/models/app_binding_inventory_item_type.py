from typing import Literal

AppBindingInventoryItemType = Literal["object_storage", "outbound", "postgres", "queue", "service"]

APP_BINDING_INVENTORY_ITEM_TYPE_VALUES: set[AppBindingInventoryItemType] = {
    "object_storage",
    "outbound",
    "postgres",
    "queue",
    "service",
}


def check_app_binding_inventory_item_type(value: str) -> AppBindingInventoryItemType:
    if value in APP_BINDING_INVENTORY_ITEM_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_BINDING_INVENTORY_ITEM_TYPE_VALUES!r}")
