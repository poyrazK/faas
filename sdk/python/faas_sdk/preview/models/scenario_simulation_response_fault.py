from typing import Literal

ScenarioSimulationResponseFault = Literal["disconnect", "timeout"]

SCENARIO_SIMULATION_RESPONSE_FAULT_VALUES: set[ScenarioSimulationResponseFault] = {
    "disconnect",
    "timeout",
}


def check_scenario_simulation_response_fault(value: str) -> ScenarioSimulationResponseFault:
    if value in SCENARIO_SIMULATION_RESPONSE_FAULT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SIMULATION_RESPONSE_FAULT_VALUES!r}")
