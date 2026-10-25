from typing import Literal

ObjectBucketVersioningRequestStatus = Literal["Enabled", "Suspended"]

OBJECT_BUCKET_VERSIONING_REQUEST_STATUS_VALUES: set[ObjectBucketVersioningRequestStatus] = {
    "Enabled",
    "Suspended",
}


def check_object_bucket_versioning_request_status(value: str) -> ObjectBucketVersioningRequestStatus:
    if value in OBJECT_BUCKET_VERSIONING_REQUEST_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_VERSIONING_REQUEST_STATUS_VALUES!r}")
