from typing import Literal

ScenarioSimulationFixtureAdapter = Literal["http", "resend"]

SCENARIO_SIMULATION_FIXTURE_ADAPTER_VALUES: set[ScenarioSimulationFixtureAdapter] = {
    "http",
    "resend",
}


def check_scenario_simulation_fixture_adapter(value: str) -> ScenarioSimulationFixtureAdapter:
    if value in SCENARIO_SIMULATION_FIXTURE_ADAPTER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SIMULATION_FIXTURE_ADAPTER_VALUES!r}")
