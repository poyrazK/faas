from typing import Literal

AutomationCheckExpectationState = Literal[
    "blocked",
    "dead",
    "error",
    "expanded",
    "failed",
    "mocked",
    "resolved",
    "skipped",
    "timed_out",
    "would_execute",
    "would_retry",
    "would_wait",
]

AUTOMATION_CHECK_EXPECTATION_STATE_VALUES: set[AutomationCheckExpectationState] = {
    "blocked",
    "dead",
    "error",
    "expanded",
    "failed",
    "mocked",
    "resolved",
    "skipped",
    "timed_out",
    "would_execute",
    "would_retry",
    "would_wait",
}


def check_automation_check_expectation_state(value: str) -> AutomationCheckExpectationState:
    if value in AUTOMATION_CHECK_EXPECTATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_CHECK_EXPECTATION_STATE_VALUES!r}")
