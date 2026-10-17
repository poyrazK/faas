from typing import Literal

OperationApprovalRequestDecision = Literal["approved", "rejected"]

OPERATION_APPROVAL_REQUEST_DECISION_VALUES: set[OperationApprovalRequestDecision] = {
    "approved",
    "rejected",
}


def check_operation_approval_request_decision(value: str) -> OperationApprovalRequestDecision:
    if value in OPERATION_APPROVAL_REQUEST_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_APPROVAL_REQUEST_DECISION_VALUES!r}")
