from typing import Literal

AutomationSimulationStepState = Literal[
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

AUTOMATION_SIMULATION_STEP_STATE_VALUES: set[AutomationSimulationStepState] = {
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


def check_automation_simulation_step_state(value: str) -> AutomationSimulationStepState:
    if value in AUTOMATION_SIMULATION_STEP_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_SIMULATION_STEP_STATE_VALUES!r}")
