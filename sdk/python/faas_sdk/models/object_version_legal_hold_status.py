from typing import Literal

ObjectVersionLegalHoldStatus = Literal["OFF", "ON"]

OBJECT_VERSION_LEGAL_HOLD_STATUS_VALUES: set[ObjectVersionLegalHoldStatus] = {
    "OFF",
    "ON",
}


def check_object_version_legal_hold_status(value: str) -> ObjectVersionLegalHoldStatus:
    if value in OBJECT_VERSION_LEGAL_HOLD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_VERSION_LEGAL_HOLD_STATUS_VALUES!r}")
