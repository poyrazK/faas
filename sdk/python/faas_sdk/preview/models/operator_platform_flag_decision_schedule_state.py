from typing import Literal

OperatorPlatformFlagDecisionScheduleState = Literal["rule_ended", "rule_not_started"]

OPERATOR_PLATFORM_FLAG_DECISION_SCHEDULE_STATE_VALUES: set[OperatorPlatformFlagDecisionScheduleState] = {
    "rule_ended",
    "rule_not_started",
}


def check_operator_platform_flag_decision_schedule_state(value: str) -> OperatorPlatformFlagDecisionScheduleState:
    if value in OPERATOR_PLATFORM_FLAG_DECISION_SCHEDULE_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATOR_PLATFORM_FLAG_DECISION_SCHEDULE_STATE_VALUES!r}"
    )
