from typing import Literal

ObjectLockDefaultRetentionMode = Literal["COMPLIANCE", "GOVERNANCE"]

OBJECT_LOCK_DEFAULT_RETENTION_MODE_VALUES: set[ObjectLockDefaultRetentionMode] = {
    "COMPLIANCE",
    "GOVERNANCE",
}


def check_object_lock_default_retention_mode(value: str) -> ObjectLockDefaultRetentionMode:
    if value in OBJECT_LOCK_DEFAULT_RETENTION_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_LOCK_DEFAULT_RETENTION_MODE_VALUES!r}")
