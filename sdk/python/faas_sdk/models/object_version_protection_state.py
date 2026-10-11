from typing import Literal

ObjectVersionProtectionState = Literal["applying", "failed", "ready", "waiting"]

OBJECT_VERSION_PROTECTION_STATE_VALUES: set[ObjectVersionProtectionState] = {
    "applying",
    "failed",
    "ready",
    "waiting",
}


def check_object_version_protection_state(value: str) -> ObjectVersionProtectionState:
    if value in OBJECT_VERSION_PROTECTION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_VERSION_PROTECTION_STATE_VALUES!r}")
