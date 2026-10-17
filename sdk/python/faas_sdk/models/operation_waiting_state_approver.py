from typing import Literal

OperationWaitingStateApprover = Literal["account", "customer"]

OPERATION_WAITING_STATE_APPROVER_VALUES: set[OperationWaitingStateApprover] = {
    "account",
    "customer",
}


def check_operation_waiting_state_approver(value: str) -> OperationWaitingStateApprover:
    if value in OPERATION_WAITING_STATE_APPROVER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WAITING_STATE_APPROVER_VALUES!r}")
