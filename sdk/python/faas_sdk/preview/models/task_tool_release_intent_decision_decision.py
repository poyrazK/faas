from typing import Literal

TaskToolReleaseIntentDecisionDecision = Literal["approve", "authorize", "reject", "revoke"]

TASK_TOOL_RELEASE_INTENT_DECISION_DECISION_VALUES: set[TaskToolReleaseIntentDecisionDecision] = {
    "approve",
    "authorize",
    "reject",
    "revoke",
}


def check_task_tool_release_intent_decision_decision(value: str) -> TaskToolReleaseIntentDecisionDecision:
    if value in TASK_TOOL_RELEASE_INTENT_DECISION_DECISION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_INTENT_DECISION_DECISION_VALUES!r}"
    )
