from typing import Literal

OperatorPlatformFlagDecisionOptIn = Literal["account", "org"]

OPERATOR_PLATFORM_FLAG_DECISION_OPT_IN_VALUES: set[OperatorPlatformFlagDecisionOptIn] = {
    "account",
    "org",
}


def check_operator_platform_flag_decision_opt_in(value: str) -> OperatorPlatformFlagDecisionOptIn:
    if value in OPERATOR_PLATFORM_FLAG_DECISION_OPT_IN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_PLATFORM_FLAG_DECISION_OPT_IN_VALUES!r}")
