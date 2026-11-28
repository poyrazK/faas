from typing import Literal

GetAppResourceInventoryKind = Literal["buckets", "crons", "databases", "domains", "storage_bindings"]

GET_APP_RESOURCE_INVENTORY_KIND_VALUES: set[GetAppResourceInventoryKind] = {
    "buckets",
    "crons",
    "databases",
    "domains",
    "storage_bindings",
}


def check_get_app_resource_inventory_kind(value: str) -> GetAppResourceInventoryKind:
    if value in GET_APP_RESOURCE_INVENTORY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_RESOURCE_INVENTORY_KIND_VALUES!r}")
