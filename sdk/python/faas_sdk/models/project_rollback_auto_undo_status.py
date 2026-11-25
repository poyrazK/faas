from typing import Literal

ProjectRollbackAutoUndoStatus = Literal["blocked", "pending", "ready", "started"]

PROJECT_ROLLBACK_AUTO_UNDO_STATUS_VALUES: set[ProjectRollbackAutoUndoStatus] = {
    "blocked",
    "pending",
    "ready",
    "started",
}


def check_project_rollback_auto_undo_status(value: str) -> ProjectRollbackAutoUndoStatus:
    if value in PROJECT_ROLLBACK_AUTO_UNDO_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ROLLBACK_AUTO_UNDO_STATUS_VALUES!r}")
