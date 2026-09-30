from typing import Literal

FlagDecisionReason = Literal[
    "configuration_stale", "customer_missing", "default", "disabled", "flag_missing", "rule_match"
]

FLAG_DECISION_REASON_VALUES: set[FlagDecisionReason] = {
    "configuration_stale",
    "customer_missing",
    "default",
    "disabled",
    "flag_missing",
    "rule_match",
}


def check_flag_decision_reason(value: str) -> FlagDecisionReason:
    if value in FLAG_DECISION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_DECISION_REASON_VALUES!r}")
