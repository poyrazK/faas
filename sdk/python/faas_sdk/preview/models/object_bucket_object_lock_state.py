from typing import Literal

ObjectBucketObjectLockState = Literal["applying", "ready", "waiting"]

OBJECT_BUCKET_OBJECT_LOCK_STATE_VALUES: set[ObjectBucketObjectLockState] = {
    "applying",
    "ready",
    "waiting",
}


def check_object_bucket_object_lock_state(value: str) -> ObjectBucketObjectLockState:
    if value in OBJECT_BUCKET_OBJECT_LOCK_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_OBJECT_LOCK_STATE_VALUES!r}")
