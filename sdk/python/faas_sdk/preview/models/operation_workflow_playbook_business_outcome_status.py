from typing import Literal

OperationWorkflowPlaybookBusinessOutcomeStatus = Literal["achieved", "different_outcome", "pending", "unavailable"]

OPERATION_WORKFLOW_PLAYBOOK_BUSINESS_OUTCOME_STATUS_VALUES: set[OperationWorkflowPlaybookBusinessOutcomeStatus] = {
    "achieved",
    "different_outcome",
    "pending",
    "unavailable",
}


def check_operation_workflow_playbook_business_outcome_status(
    value: str,
) -> OperationWorkflowPlaybookBusinessOutcomeStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_BUSINESS_OUTCOME_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_BUSINESS_OUTCOME_STATUS_VALUES!r}"
    )
