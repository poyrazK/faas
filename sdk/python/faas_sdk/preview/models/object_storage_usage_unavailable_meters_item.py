from typing import Literal

ObjectStorageUsageUnavailableMetersItem = Literal["cost_millicents", "stored_byte_hours"]

OBJECT_STORAGE_USAGE_UNAVAILABLE_METERS_ITEM_VALUES: set[ObjectStorageUsageUnavailableMetersItem] = {
    "cost_millicents",
    "stored_byte_hours",
}


def check_object_storage_usage_unavailable_meters_item(value: str) -> ObjectStorageUsageUnavailableMetersItem:
    if value in OBJECT_STORAGE_USAGE_UNAVAILABLE_METERS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBJECT_STORAGE_USAGE_UNAVAILABLE_METERS_ITEM_VALUES!r}"
    )
