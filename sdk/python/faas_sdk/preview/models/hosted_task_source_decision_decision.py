from typing import Literal

HostedTaskSourceDecisionDecision = Literal["approve", "reject", "revoke"]

HOSTED_TASK_SOURCE_DECISION_DECISION_VALUES: set[HostedTaskSourceDecisionDecision] = {
    "approve",
    "reject",
    "revoke",
}


def check_hosted_task_source_decision_decision(value: str) -> HostedTaskSourceDecisionDecision:
    if value in HOSTED_TASK_SOURCE_DECISION_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_SOURCE_DECISION_DECISION_VALUES!r}")
