from typing import Literal

OperationWorkflowStateReportDeadlineAtType1 = Literal[""]

OPERATION_WORKFLOW_STATE_REPORT_DEADLINE_AT_TYPE_1_VALUES: set[OperationWorkflowStateReportDeadlineAtType1] = {
    "",
}


def check_operation_workflow_state_report_deadline_at_type_1(value: str) -> OperationWorkflowStateReportDeadlineAtType1:
    if value in OPERATION_WORKFLOW_STATE_REPORT_DEADLINE_AT_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_STATE_REPORT_DEADLINE_AT_TYPE_1_VALUES!r}"
    )
