from typing import Literal

ObjectVersionRetentionEventHold = Literal["OFF", "ON"]

OBJECT_VERSION_RETENTION_EVENT_HOLD_VALUES: set[ObjectVersionRetentionEventHold] = {
    "OFF",
    "ON",
}


def check_object_version_retention_event_hold(value: str) -> ObjectVersionRetentionEventHold:
    if value in OBJECT_VERSION_RETENTION_EVENT_HOLD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_VERSION_RETENTION_EVENT_HOLD_VALUES!r}")
