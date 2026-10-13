from typing import Literal

OperationWorkflowConditionCoverageEntryStatus = Literal["missing", "overdue", "reported"]

OPERATION_WORKFLOW_CONDITION_COVERAGE_ENTRY_STATUS_VALUES: set[OperationWorkflowConditionCoverageEntryStatus] = {
    "missing",
    "overdue",
    "reported",
}


def check_operation_workflow_condition_coverage_entry_status(
    value: str,
) -> OperationWorkflowConditionCoverageEntryStatus:
    if value in OPERATION_WORKFLOW_CONDITION_COVERAGE_ENTRY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CONDITION_COVERAGE_ENTRY_STATUS_VALUES!r}"
    )
