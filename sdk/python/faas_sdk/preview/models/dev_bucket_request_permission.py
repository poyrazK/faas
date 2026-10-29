from typing import Literal

DevBucketRequestPermission = Literal["read", "read_write", "write"]

DEV_BUCKET_REQUEST_PERMISSION_VALUES: set[DevBucketRequestPermission] = {
    "read",
    "read_write",
    "write",
}


def check_dev_bucket_request_permission(value: str) -> DevBucketRequestPermission:
    if value in DEV_BUCKET_REQUEST_PERMISSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_BUCKET_REQUEST_PERMISSION_VALUES!r}")
