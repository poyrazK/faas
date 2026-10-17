from typing import Literal

ScenarioSimulationAttemptStatus = Literal["completed", "interrupted", "pending", "unsupported"]

SCENARIO_SIMULATION_ATTEMPT_STATUS_VALUES: set[ScenarioSimulationAttemptStatus] = {
    "completed",
    "interrupted",
    "pending",
    "unsupported",
}


def check_scenario_simulation_attempt_status(value: str) -> ScenarioSimulationAttemptStatus:
    if value in SCENARIO_SIMULATION_ATTEMPT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SIMULATION_ATTEMPT_STATUS_VALUES!r}")
