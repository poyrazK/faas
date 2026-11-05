from typing import Literal

AutomationSimulationMockAttemptOutcome = Literal["failure", "success", "timeout"]

AUTOMATION_SIMULATION_MOCK_ATTEMPT_OUTCOME_VALUES: set[AutomationSimulationMockAttemptOutcome] = {
    "failure",
    "success",
    "timeout",
}


def check_automation_simulation_mock_attempt_outcome(value: str) -> AutomationSimulationMockAttemptOutcome:
    if value in AUTOMATION_SIMULATION_MOCK_ATTEMPT_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {AUTOMATION_SIMULATION_MOCK_ATTEMPT_OUTCOME_VALUES!r}"
    )
