from typing import Literal

FailureRuleAction = Literal["fail_partition", "retry"]

FAILURE_RULE_ACTION_VALUES: set[FailureRuleAction] = {
    "fail_partition",
    "retry",
}


def check_failure_rule_action(value: str) -> FailureRuleAction:
    if value in FAILURE_RULE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FAILURE_RULE_ACTION_VALUES!r}")
