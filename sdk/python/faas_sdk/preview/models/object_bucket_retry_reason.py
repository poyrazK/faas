from typing import Literal

ObjectBucketRetryReason = Literal["cleanup_pending", "configuration", "conflict", "invalid", "protected", "temporary"]

OBJECT_BUCKET_RETRY_REASON_VALUES: set[ObjectBucketRetryReason] = {
    "cleanup_pending",
    "configuration",
    "conflict",
    "invalid",
    "protected",
    "temporary",
}


def check_object_bucket_retry_reason(value: str) -> ObjectBucketRetryReason:
    if value in OBJECT_BUCKET_RETRY_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_RETRY_REASON_VALUES!r}")
