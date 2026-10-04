from typing import Literal

ObjectDeletionLastErrorCode = Literal[
    "configuration", "preparation_expired", "preparation_failed", "provider_rejected", "provider_uncertain"
]

OBJECT_DELETION_LAST_ERROR_CODE_VALUES: set[ObjectDeletionLastErrorCode] = {
    "configuration",
    "preparation_expired",
    "preparation_failed",
    "provider_rejected",
    "provider_uncertain",
}


def check_object_deletion_last_error_code(value: str) -> ObjectDeletionLastErrorCode:
    if value in OBJECT_DELETION_LAST_ERROR_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_DELETION_LAST_ERROR_CODE_VALUES!r}")
