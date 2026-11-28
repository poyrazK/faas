from typing import Literal

ObjectBucketAccessGrantPermission = Literal["read", "read_write", "write"]

OBJECT_BUCKET_ACCESS_GRANT_PERMISSION_VALUES: set[ObjectBucketAccessGrantPermission] = {
    "read",
    "read_write",
    "write",
}


def check_object_bucket_access_grant_permission(value: str) -> ObjectBucketAccessGrantPermission:
    if value in OBJECT_BUCKET_ACCESS_GRANT_PERMISSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_ACCESS_GRANT_PERMISSION_VALUES!r}")
