from typing import Literal

OperationWorkflowStateSLAStatus = Literal["at_risk", "breached", "unknown", "within_budget"]

OPERATION_WORKFLOW_STATE_SLA_STATUS_VALUES: set[OperationWorkflowStateSLAStatus] = {
    "at_risk",
    "breached",
    "unknown",
    "within_budget",
}


def check_operation_workflow_state_sla_status(value: str) -> OperationWorkflowStateSLAStatus:
    if value in OPERATION_WORKFLOW_STATE_SLA_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_STATE_SLA_STATUS_VALUES!r}")
