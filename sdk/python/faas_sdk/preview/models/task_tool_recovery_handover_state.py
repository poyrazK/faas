from typing import Literal

TaskToolRecoveryHandoverState = Literal["settled", "stopping"]

TASK_TOOL_RECOVERY_HANDOVER_STATE_VALUES: set[TaskToolRecoveryHandoverState] = {
    "settled",
    "stopping",
}


def check_task_tool_recovery_handover_state(value: str) -> TaskToolRecoveryHandoverState:
    if value in TASK_TOOL_RECOVERY_HANDOVER_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RECOVERY_HANDOVER_STATE_VALUES!r}")
