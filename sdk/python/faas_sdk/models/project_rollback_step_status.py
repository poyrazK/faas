from typing import Literal

ProjectRollbackStepStatus = Literal["blocked", "failed", "in_progress", "planned", "rolled_back", "skipped"]

PROJECT_ROLLBACK_STEP_STATUS_VALUES: set[ProjectRollbackStepStatus] = {
    "blocked",
    "failed",
    "in_progress",
    "planned",
    "rolled_back",
    "skipped",
}


def check_project_rollback_step_status(value: str) -> ProjectRollbackStepStatus:
    if value in PROJECT_ROLLBACK_STEP_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ROLLBACK_STEP_STATUS_VALUES!r}")
