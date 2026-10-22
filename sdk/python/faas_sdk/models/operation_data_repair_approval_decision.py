from typing import Literal

OperationDataRepairApprovalDecision = Literal["approved", "rejected"]

OPERATION_DATA_REPAIR_APPROVAL_DECISION_VALUES: set[OperationDataRepairApprovalDecision] = {
    "approved",
    "rejected",
}


def check_operation_data_repair_approval_decision(value: str) -> OperationDataRepairApprovalDecision:
    if value in OPERATION_DATA_REPAIR_APPROVAL_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DATA_REPAIR_APPROVAL_DECISION_VALUES!r}")
