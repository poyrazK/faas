from typing import Literal

ObjectVersionRetentionMode = Literal["COMPLIANCE", "GOVERNANCE"]

OBJECT_VERSION_RETENTION_MODE_VALUES: set[ObjectVersionRetentionMode] = {
    "COMPLIANCE",
    "GOVERNANCE",
}


def check_object_version_retention_mode(value: str) -> ObjectVersionRetentionMode:
    if value in OBJECT_VERSION_RETENTION_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_VERSION_RETENTION_MODE_VALUES!r}")
