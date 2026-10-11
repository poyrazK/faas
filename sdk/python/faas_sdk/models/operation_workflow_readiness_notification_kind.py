from typing import Literal

OperationWorkflowReadinessNotificationKind = Literal[
    "newly_blocked",
    "newly_ready",
    "outcome_achieved",
    "outcome_deadline_reached",
    "outcome_different_outcome",
    "outcome_unavailable",
    "reassigned",
    "unavailable",
    "verification_invalidated",
    "verification_unavailable",
    "verification_verified",
]

OPERATION_WORKFLOW_READINESS_NOTIFICATION_KIND_VALUES: set[OperationWorkflowReadinessNotificationKind] = {
    "newly_blocked",
    "newly_ready",
    "outcome_achieved",
    "outcome_deadline_reached",
    "outcome_different_outcome",
    "outcome_unavailable",
    "reassigned",
    "unavailable",
    "verification_invalidated",
    "verification_unavailable",
    "verification_verified",
}


def check_operation_workflow_readiness_notification_kind(value: str) -> OperationWorkflowReadinessNotificationKind:
    if value in OPERATION_WORKFLOW_READINESS_NOTIFICATION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_NOTIFICATION_KIND_VALUES!r}"
    )
