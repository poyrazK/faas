from typing import Literal

AutomationSimulationAttemptOutcome = Literal["failure", "success", "timeout"]

AUTOMATION_SIMULATION_ATTEMPT_OUTCOME_VALUES: set[AutomationSimulationAttemptOutcome] = {
    "failure",
    "success",
    "timeout",
}


def check_automation_simulation_attempt_outcome(value: str) -> AutomationSimulationAttemptOutcome:
    if value in AUTOMATION_SIMULATION_ATTEMPT_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_SIMULATION_ATTEMPT_OUTCOME_VALUES!r}")
