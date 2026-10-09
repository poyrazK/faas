from typing import Literal

WorkflowQueuedRunCancelOutcomeOutcome = Literal[
    "already_cancelled", "already_started", "cancelled", "eligible", "not_found", "not_queued", "workflow_mismatch"
]

WORKFLOW_QUEUED_RUN_CANCEL_OUTCOME_OUTCOME_VALUES: set[WorkflowQueuedRunCancelOutcomeOutcome] = {
    "already_cancelled",
    "already_started",
    "cancelled",
    "eligible",
    "not_found",
    "not_queued",
    "workflow_mismatch",
}


def check_workflow_queued_run_cancel_outcome_outcome(value: str) -> WorkflowQueuedRunCancelOutcomeOutcome:
    if value in WORKFLOW_QUEUED_RUN_CANCEL_OUTCOME_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_QUEUED_RUN_CANCEL_OUTCOME_OUTCOME_VALUES!r}"
    )
