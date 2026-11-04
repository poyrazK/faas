from typing import Literal

ObjectBucketVersioningObservedStatus = Literal["", "Enabled", "Suspended"]

OBJECT_BUCKET_VERSIONING_OBSERVED_STATUS_VALUES: set[ObjectBucketVersioningObservedStatus] = {
    "",
    "Enabled",
    "Suspended",
}


def check_object_bucket_versioning_observed_status(value: str) -> ObjectBucketVersioningObservedStatus:
    if value in OBJECT_BUCKET_VERSIONING_OBSERVED_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_VERSIONING_OBSERVED_STATUS_VALUES!r}")
