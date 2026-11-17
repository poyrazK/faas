from typing import Literal

TaskToolRecoveryQualificationAction = Literal["quarantine", "restore", "retire"]

TASK_TOOL_RECOVERY_QUALIFICATION_ACTION_VALUES: set[TaskToolRecoveryQualificationAction] = {
    "quarantine",
    "restore",
    "retire",
}


def check_task_tool_recovery_qualification_action(value: str) -> TaskToolRecoveryQualificationAction:
    if value in TASK_TOOL_RECOVERY_QUALIFICATION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RECOVERY_QUALIFICATION_ACTION_VALUES!r}")
