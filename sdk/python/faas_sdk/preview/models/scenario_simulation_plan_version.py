from typing import Literal

ScenarioSimulationPlanVersion = Literal[1]

SCENARIO_SIMULATION_PLAN_VERSION_VALUES: set[ScenarioSimulationPlanVersion] = {
    1,
}


def check_scenario_simulation_plan_version(value: int) -> ScenarioSimulationPlanVersion:
    if value in SCENARIO_SIMULATION_PLAN_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SIMULATION_PLAN_VERSION_VALUES!r}")
