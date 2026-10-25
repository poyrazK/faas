from typing import Literal

SetObjectBucketAccessGrantRequestPermission = Literal["read", "read_write", "write"]

SET_OBJECT_BUCKET_ACCESS_GRANT_REQUEST_PERMISSION_VALUES: set[SetObjectBucketAccessGrantRequestPermission] = {
    "read",
    "read_write",
    "write",
}


def check_set_object_bucket_access_grant_request_permission(value: str) -> SetObjectBucketAccessGrantRequestPermission:
    if value in SET_OBJECT_BUCKET_ACCESS_GRANT_REQUEST_PERMISSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_OBJECT_BUCKET_ACCESS_GRANT_REQUEST_PERMISSION_VALUES!r}"
    )
