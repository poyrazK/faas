from typing import Literal

TaskToolReleaseIntentDecisionDecision = Literal[
    "approve",
    "authorize",
    "recovery_approve",
    "recovery_qualify",
    "recovery_quarantine_approve",
    "recovery_quarantine_qualify",
    "recovery_quarantine_revoke",
    "recovery_retire_approve",
    "recovery_retire_qualify",
    "recovery_retire_revoke",
    "recovery_revoke",
    "recovery_stop",
    "reject",
    "revoke",
]

TASK_TOOL_RELEASE_INTENT_DECISION_DECISION_VALUES: set[TaskToolReleaseIntentDecisionDecision] = {
    "approve",
    "authorize",
    "recovery_approve",
    "recovery_qualify",
    "recovery_quarantine_approve",
    "recovery_quarantine_qualify",
    "recovery_quarantine_revoke",
    "recovery_retire_approve",
    "recovery_retire_qualify",
    "recovery_retire_revoke",
    "recovery_revoke",
    "recovery_stop",
    "reject",
    "revoke",
}


def check_task_tool_release_intent_decision_decision(value: str) -> TaskToolReleaseIntentDecisionDecision:
    if value in TASK_TOOL_RELEASE_INTENT_DECISION_DECISION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_INTENT_DECISION_DECISION_VALUES!r}"
    )
