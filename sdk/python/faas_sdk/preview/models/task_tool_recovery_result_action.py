from typing import Literal

TaskToolRecoveryResultAction = Literal["restore"]

TASK_TOOL_RECOVERY_RESULT_ACTION_VALUES: set[TaskToolRecoveryResultAction] = {
    "restore",
}


def check_task_tool_recovery_result_action(value: str) -> TaskToolRecoveryResultAction:
    if value in TASK_TOOL_RECOVERY_RESULT_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RECOVERY_RESULT_ACTION_VALUES!r}")
