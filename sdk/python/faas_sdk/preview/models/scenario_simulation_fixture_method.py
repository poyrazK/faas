from typing import Literal

ScenarioSimulationFixtureMethod = Literal["DELETE", "GET", "HEAD", "PATCH", "POST", "PUT"]

SCENARIO_SIMULATION_FIXTURE_METHOD_VALUES: set[ScenarioSimulationFixtureMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "PATCH",
    "POST",
    "PUT",
}


def check_scenario_simulation_fixture_method(value: str) -> ScenarioSimulationFixtureMethod:
    if value in SCENARIO_SIMULATION_FIXTURE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SIMULATION_FIXTURE_METHOD_VALUES!r}")
