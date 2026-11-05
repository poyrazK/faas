from typing import Literal

WorkflowRecoveryOverviewStatus = Literal["healthy", "inactive", "needs_attention", "paused", "unknown", "waiting"]

WORKFLOW_RECOVERY_OVERVIEW_STATUS_VALUES: set[WorkflowRecoveryOverviewStatus] = {
    "healthy",
    "inactive",
    "needs_attention",
    "paused",
    "unknown",
    "waiting",
}


def check_workflow_recovery_overview_status(value: str) -> WorkflowRecoveryOverviewStatus:
    if value in WORKFLOW_RECOVERY_OVERVIEW_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_OVERVIEW_STATUS_VALUES!r}")
