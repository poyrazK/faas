from typing import Literal

AutomationFailureTransitionReason = Literal["failure_threshold", "operator_resume"]

AUTOMATION_FAILURE_TRANSITION_REASON_VALUES: set[AutomationFailureTransitionReason] = {
    "failure_threshold",
    "operator_resume",
}


def check_automation_failure_transition_reason(value: str) -> AutomationFailureTransitionReason:
    if value in AUTOMATION_FAILURE_TRANSITION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_FAILURE_TRANSITION_REASON_VALUES!r}")
