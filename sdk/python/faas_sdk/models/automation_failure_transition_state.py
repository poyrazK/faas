from typing import Literal

AutomationFailureTransitionState = Literal["paused", "resumed"]

AUTOMATION_FAILURE_TRANSITION_STATE_VALUES: set[AutomationFailureTransitionState] = {
    "paused",
    "resumed",
}


def check_automation_failure_transition_state(value: str) -> AutomationFailureTransitionState:
    if value in AUTOMATION_FAILURE_TRANSITION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_FAILURE_TRANSITION_STATE_VALUES!r}")
