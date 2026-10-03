from typing import Literal

ObjectBucketListUploadProfile = Literal["direct", "proxied"]

OBJECT_BUCKET_LIST_UPLOAD_PROFILE_VALUES: set[ObjectBucketListUploadProfile] = {
    "direct",
    "proxied",
}


def check_object_bucket_list_upload_profile(value: str) -> ObjectBucketListUploadProfile:
    if value in OBJECT_BUCKET_LIST_UPLOAD_PROFILE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_LIST_UPLOAD_PROFILE_VALUES!r}")
