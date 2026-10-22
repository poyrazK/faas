from typing import Literal

ObjectStorageComputeTriggerEventsItem = Literal[
    "s3:ObjectCreated:*", "s3:ObjectCreated:CompleteMultipartUpload", "s3:ObjectCreated:Copy", "s3:ObjectCreated:Put"
]

OBJECT_STORAGE_COMPUTE_TRIGGER_EVENTS_ITEM_VALUES: set[ObjectStorageComputeTriggerEventsItem] = {
    "s3:ObjectCreated:*",
    "s3:ObjectCreated:CompleteMultipartUpload",
    "s3:ObjectCreated:Copy",
    "s3:ObjectCreated:Put",
}


def check_object_storage_compute_trigger_events_item(value: str) -> ObjectStorageComputeTriggerEventsItem:
    if value in OBJECT_STORAGE_COMPUTE_TRIGGER_EVENTS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBJECT_STORAGE_COMPUTE_TRIGGER_EVENTS_ITEM_VALUES!r}"
    )
