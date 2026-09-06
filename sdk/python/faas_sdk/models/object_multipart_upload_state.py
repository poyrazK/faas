from typing import Literal

ObjectMultipartUploadState = Literal["aborted", "aborting", "active", "completed", "completing", "initiating"]

OBJECT_MULTIPART_UPLOAD_STATE_VALUES: set[ObjectMultipartUploadState] = {
    "aborted",
    "aborting",
    "active",
    "completed",
    "completing",
    "initiating",
}


def check_object_multipart_upload_state(value: str) -> ObjectMultipartUploadState:
    if value in OBJECT_MULTIPART_UPLOAD_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_MULTIPART_UPLOAD_STATE_VALUES!r}")
