from typing import Literal

ObjectBucketEncryptionState = Literal["applying", "ready", "waiting"]

OBJECT_BUCKET_ENCRYPTION_STATE_VALUES: set[ObjectBucketEncryptionState] = {
    "applying",
    "ready",
    "waiting",
}


def check_object_bucket_encryption_state(value: str) -> ObjectBucketEncryptionState:
    if value in OBJECT_BUCKET_ENCRYPTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_ENCRYPTION_STATE_VALUES!r}")
