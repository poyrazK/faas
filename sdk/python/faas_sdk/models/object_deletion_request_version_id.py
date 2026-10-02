from typing import Literal

ObjectDeletionRequestVersionId = Literal["null"]

OBJECT_DELETION_REQUEST_VERSION_ID_VALUES: set[ObjectDeletionRequestVersionId] = {
    "null",
}


def check_object_deletion_request_version_id(value: str) -> ObjectDeletionRequestVersionId:
    if value in OBJECT_DELETION_REQUEST_VERSION_ID_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_DELETION_REQUEST_VERSION_ID_VALUES!r}")
