from typing import Literal

OperationWaitingStateKind = Literal["approval", "event", "timer"]

OPERATION_WAITING_STATE_KIND_VALUES: set[OperationWaitingStateKind] = {
    "approval",
    "event",
    "timer",
}


def check_operation_waiting_state_kind(value: str) -> OperationWaitingStateKind:
    if value in OPERATION_WAITING_STATE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WAITING_STATE_KIND_VALUES!r}")
