from typing import Literal

ObjectLifecycleScanState = Literal["cancelled", "completed", "scanning"]

OBJECT_LIFECYCLE_SCAN_STATE_VALUES: set[ObjectLifecycleScanState] = {
    "cancelled",
    "completed",
    "scanning",
}


def check_object_lifecycle_scan_state(value: str) -> ObjectLifecycleScanState:
    if value in OBJECT_LIFECYCLE_SCAN_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_LIFECYCLE_SCAN_STATE_VALUES!r}")
