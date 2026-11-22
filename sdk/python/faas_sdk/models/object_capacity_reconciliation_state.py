from typing import Literal

ObjectCapacityReconciliationState = Literal["blocked", "cancelled", "completed", "failed", "scanning", "waiting"]

OBJECT_CAPACITY_RECONCILIATION_STATE_VALUES: set[ObjectCapacityReconciliationState] = {
    "blocked",
    "cancelled",
    "completed",
    "failed",
    "scanning",
    "waiting",
}


def check_object_capacity_reconciliation_state(value: str) -> ObjectCapacityReconciliationState:
    if value in OBJECT_CAPACITY_RECONCILIATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_CAPACITY_RECONCILIATION_STATE_VALUES!r}")
