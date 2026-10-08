from typing import Literal

OperationWorkflowTransitionReadinessAdvisoriesItem = Literal["deadline_overdue", "state_stale"]

OPERATION_WORKFLOW_TRANSITION_READINESS_ADVISORIES_ITEM_VALUES: set[
    OperationWorkflowTransitionReadinessAdvisoriesItem
] = {
    "deadline_overdue",
    "state_stale",
}


def check_operation_workflow_transition_readiness_advisories_item(
    value: str,
) -> OperationWorkflowTransitionReadinessAdvisoriesItem:
    if value in OPERATION_WORKFLOW_TRANSITION_READINESS_ADVISORIES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_TRANSITION_READINESS_ADVISORIES_ITEM_VALUES!r}"
    )
