from typing import Literal

ProjectRollbackOperationStatus = Literal["blocked", "complete", "planned", "running"]

PROJECT_ROLLBACK_OPERATION_STATUS_VALUES: set[ProjectRollbackOperationStatus] = {
    "blocked",
    "complete",
    "planned",
    "running",
}


def check_project_rollback_operation_status(value: str) -> ProjectRollbackOperationStatus:
    if value in PROJECT_ROLLBACK_OPERATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ROLLBACK_OPERATION_STATUS_VALUES!r}")
