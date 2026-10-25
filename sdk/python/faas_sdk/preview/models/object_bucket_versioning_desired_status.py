from typing import Literal

ObjectBucketVersioningDesiredStatus = Literal["", "Enabled", "Suspended"]

OBJECT_BUCKET_VERSIONING_DESIRED_STATUS_VALUES: set[ObjectBucketVersioningDesiredStatus] = {
    "",
    "Enabled",
    "Suspended",
}


def check_object_bucket_versioning_desired_status(value: str) -> ObjectBucketVersioningDesiredStatus:
    if value in OBJECT_BUCKET_VERSIONING_DESIRED_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_VERSIONING_DESIRED_STATUS_VALUES!r}")
