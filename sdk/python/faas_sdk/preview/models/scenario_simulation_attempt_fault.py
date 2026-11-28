from typing import Literal

ScenarioSimulationAttemptFault = Literal["disconnect", "timeout"]

SCENARIO_SIMULATION_ATTEMPT_FAULT_VALUES: set[ScenarioSimulationAttemptFault] = {
    "disconnect",
    "timeout",
}


def check_scenario_simulation_attempt_fault(value: str) -> ScenarioSimulationAttemptFault:
    if value in SCENARIO_SIMULATION_ATTEMPT_FAULT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SIMULATION_ATTEMPT_FAULT_VALUES!r}")
