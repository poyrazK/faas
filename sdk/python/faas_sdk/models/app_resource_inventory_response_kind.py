from typing import Literal

AppResourceInventoryResponseKind = Literal["buckets", "crons", "databases", "domains", "storage_bindings"]

APP_RESOURCE_INVENTORY_RESPONSE_KIND_VALUES: set[AppResourceInventoryResponseKind] = {
    "buckets",
    "crons",
    "databases",
    "domains",
    "storage_bindings",
}


def check_app_resource_inventory_response_kind(value: str) -> AppResourceInventoryResponseKind:
    if value in APP_RESOURCE_INVENTORY_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESOURCE_INVENTORY_RESPONSE_KIND_VALUES!r}")
