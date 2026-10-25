from typing import Literal

ObjectLifecycleScanPhase = Literal["multipart", "objects"]

OBJECT_LIFECYCLE_SCAN_PHASE_VALUES: set[ObjectLifecycleScanPhase] = {
    "multipart",
    "objects",
}


def check_object_lifecycle_scan_phase(value: str) -> ObjectLifecycleScanPhase:
    if value in OBJECT_LIFECYCLE_SCAN_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_LIFECYCLE_SCAN_PHASE_VALUES!r}")
