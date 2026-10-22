from typing import Literal

OperatorPlatformFlagDecisionReason = Literal[
    "account_missing", "default", "flag_disabled", "opt_in", "rule_match", "unknown_flag"
]

OPERATOR_PLATFORM_FLAG_DECISION_REASON_VALUES: set[OperatorPlatformFlagDecisionReason] = {
    "account_missing",
    "default",
    "flag_disabled",
    "opt_in",
    "rule_match",
    "unknown_flag",
}


def check_operator_platform_flag_decision_reason(value: str) -> OperatorPlatformFlagDecisionReason:
    if value in OPERATOR_PLATFORM_FLAG_DECISION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_PLATFORM_FLAG_DECISION_REASON_VALUES!r}")
