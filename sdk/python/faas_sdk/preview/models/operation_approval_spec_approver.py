from typing import Literal

OperationApprovalSpecApprover = Literal["account", "customer"]

OPERATION_APPROVAL_SPEC_APPROVER_VALUES: set[OperationApprovalSpecApprover] = {
    "account",
    "customer",
}


def check_operation_approval_spec_approver(value: str) -> OperationApprovalSpecApprover:
    if value in OPERATION_APPROVAL_SPEC_APPROVER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_APPROVAL_SPEC_APPROVER_VALUES!r}")
