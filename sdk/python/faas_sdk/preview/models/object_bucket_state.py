from typing import Literal

ObjectBucketState = Literal["deleting", "provisioning", "ready"]

OBJECT_BUCKET_STATE_VALUES: set[ObjectBucketState] = {
    "deleting",
    "provisioning",
    "ready",
}


def check_object_bucket_state(value: str) -> ObjectBucketState:
    if value in OBJECT_BUCKET_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_STATE_VALUES!r}")
