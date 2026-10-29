from typing import Literal

RollbackOperationTrigger = Literal["alert", "auto_5xx", "manual"]

ROLLBACK_OPERATION_TRIGGER_VALUES: set[RollbackOperationTrigger] = {
    "alert",
    "auto_5xx",
    "manual",
}


def check_rollback_operation_trigger(value: str) -> RollbackOperationTrigger:
    if value in ROLLBACK_OPERATION_TRIGGER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROLLBACK_OPERATION_TRIGGER_VALUES!r}")
