from typing import Literal

HostedTaskRehearsalDecisionDecision = Literal["approve", "revoke"]

HOSTED_TASK_REHEARSAL_DECISION_DECISION_VALUES: set[HostedTaskRehearsalDecisionDecision] = {
    "approve",
    "revoke",
}


def check_hosted_task_rehearsal_decision_decision(value: str) -> HostedTaskRehearsalDecisionDecision:
    if value in HOSTED_TASK_REHEARSAL_DECISION_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_REHEARSAL_DECISION_DECISION_VALUES!r}")
