from typing import Literal

AutomationCheckAttemptOutcome = Literal["failure", "success", "timeout"]

AUTOMATION_CHECK_ATTEMPT_OUTCOME_VALUES: set[AutomationCheckAttemptOutcome] = {
    "failure",
    "success",
    "timeout",
}


def check_automation_check_attempt_outcome(value: str) -> AutomationCheckAttemptOutcome:
    if value in AUTOMATION_CHECK_ATTEMPT_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_CHECK_ATTEMPT_OUTCOME_VALUES!r}")
