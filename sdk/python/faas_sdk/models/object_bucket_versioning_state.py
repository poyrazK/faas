from typing import Literal

ObjectBucketVersioningState = Literal["inventory", "propagating", "ready", "waiting"]

OBJECT_BUCKET_VERSIONING_STATE_VALUES: set[ObjectBucketVersioningState] = {
    "inventory",
    "propagating",
    "ready",
    "waiting",
}


def check_object_bucket_versioning_state(value: str) -> ObjectBucketVersioningState:
    if value in OBJECT_BUCKET_VERSIONING_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_VERSIONING_STATE_VALUES!r}")
