from typing import Literal

AutomationLiveExpectationState = Literal["dead", "failed", "skipped", "succeeded"]

AUTOMATION_LIVE_EXPECTATION_STATE_VALUES: set[AutomationLiveExpectationState] = {
    "dead",
    "failed",
    "skipped",
    "succeeded",
}


def check_automation_live_expectation_state(value: str) -> AutomationLiveExpectationState:
    if value in AUTOMATION_LIVE_EXPECTATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_EXPECTATION_STATE_VALUES!r}")
