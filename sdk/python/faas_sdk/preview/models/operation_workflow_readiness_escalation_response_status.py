from typing import Literal

OperationWorkflowReadinessEscalationResponseStatus = Literal["acknowledged", "awaiting", "closed", "deadline_missed"]

OPERATION_WORKFLOW_READINESS_ESCALATION_RESPONSE_STATUS_VALUES: set[
    OperationWorkflowReadinessEscalationResponseStatus
] = {
    "acknowledged",
    "awaiting",
    "closed",
    "deadline_missed",
}


def check_operation_workflow_readiness_escalation_response_status(
    value: str,
) -> OperationWorkflowReadinessEscalationResponseStatus:
    if value in OPERATION_WORKFLOW_READINESS_ESCALATION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_ESCALATION_RESPONSE_STATUS_VALUES!r}"
    )
