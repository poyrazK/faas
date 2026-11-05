from typing import Literal

WorkflowRecoveryAutomaticQueueEntryWaitCategory = Literal[
    "budget", "cooldown", "evidence", "execution", "policy", "worker"
]

WORKFLOW_RECOVERY_AUTOMATIC_QUEUE_ENTRY_WAIT_CATEGORY_VALUES: set[WorkflowRecoveryAutomaticQueueEntryWaitCategory] = {
    "budget",
    "cooldown",
    "evidence",
    "execution",
    "policy",
    "worker",
}


def check_workflow_recovery_automatic_queue_entry_wait_category(
    value: str,
) -> WorkflowRecoveryAutomaticQueueEntryWaitCategory:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_QUEUE_ENTRY_WAIT_CATEGORY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_QUEUE_ENTRY_WAIT_CATEGORY_VALUES!r}"
    )
