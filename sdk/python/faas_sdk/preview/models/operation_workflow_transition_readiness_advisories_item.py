from typing import Literal

OperationWorkflowTransitionReadinessAdvisoriesItem = Literal[
    "condition_reporting_unknown",
    "condition_reports_missing",
    "condition_reports_overdue",
    "deadline_overdue",
    "state_stale",
    "verification_pending",
]

OPERATION_WORKFLOW_TRANSITION_READINESS_ADVISORIES_ITEM_VALUES: set[
    OperationWorkflowTransitionReadinessAdvisoriesItem
] = {
    "condition_reporting_unknown",
    "condition_reports_missing",
    "condition_reports_overdue",
    "deadline_overdue",
    "state_stale",
    "verification_pending",
}


def check_operation_workflow_transition_readiness_advisories_item(
    value: str,
) -> OperationWorkflowTransitionReadinessAdvisoriesItem:
    if value in OPERATION_WORKFLOW_TRANSITION_READINESS_ADVISORIES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_TRANSITION_READINESS_ADVISORIES_ITEM_VALUES!r}"
    )
