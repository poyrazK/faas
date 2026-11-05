from typing import Literal

ObjectBucketAccessGrantKeyStatus = Literal["active", "grace", "revoked"]

OBJECT_BUCKET_ACCESS_GRANT_KEY_STATUS_VALUES: set[ObjectBucketAccessGrantKeyStatus] = {
    "active",
    "grace",
    "revoked",
}


def check_object_bucket_access_grant_key_status(value: str) -> ObjectBucketAccessGrantKeyStatus:
    if value in OBJECT_BUCKET_ACCESS_GRANT_KEY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_ACCESS_GRANT_KEY_STATUS_VALUES!r}")
