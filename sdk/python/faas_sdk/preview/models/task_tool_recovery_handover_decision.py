from typing import Literal

TaskToolRecoveryHandoverDecision = Literal["approved", "revoked"]

TASK_TOOL_RECOVERY_HANDOVER_DECISION_VALUES: set[TaskToolRecoveryHandoverDecision] = {
    "approved",
    "revoked",
}


def check_task_tool_recovery_handover_decision(value: str) -> TaskToolRecoveryHandoverDecision:
    if value in TASK_TOOL_RECOVERY_HANDOVER_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RECOVERY_HANDOVER_DECISION_VALUES!r}")
