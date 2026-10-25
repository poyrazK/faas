from typing import Literal

ObjectDeletionState = Literal["completed", "dispatched", "failed", "prepared"]

OBJECT_DELETION_STATE_VALUES: set[ObjectDeletionState] = {
    "completed",
    "dispatched",
    "failed",
    "prepared",
}


def check_object_deletion_state(value: str) -> ObjectDeletionState:
    if value in OBJECT_DELETION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_DELETION_STATE_VALUES!r}")
