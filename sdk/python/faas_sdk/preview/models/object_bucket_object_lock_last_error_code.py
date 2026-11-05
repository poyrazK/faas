from typing import Literal

ObjectBucketObjectLockLastErrorCode = Literal[
    "capacity_active",
    "multipart_active",
    "provider_failed",
    "provider_mismatch",
    "provider_unsupported",
    "unsettled_writes",
    "untracked_writes",
    "versioning_pending",
]

OBJECT_BUCKET_OBJECT_LOCK_LAST_ERROR_CODE_VALUES: set[ObjectBucketObjectLockLastErrorCode] = {
    "capacity_active",
    "multipart_active",
    "provider_failed",
    "provider_mismatch",
    "provider_unsupported",
    "unsettled_writes",
    "untracked_writes",
    "versioning_pending",
}


def check_object_bucket_object_lock_last_error_code(value: str) -> ObjectBucketObjectLockLastErrorCode:
    if value in OBJECT_BUCKET_OBJECT_LOCK_LAST_ERROR_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_OBJECT_LOCK_LAST_ERROR_CODE_VALUES!r}")
