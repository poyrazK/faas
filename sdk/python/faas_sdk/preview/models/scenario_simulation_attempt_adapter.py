from typing import Literal

ScenarioSimulationAttemptAdapter = Literal["http", "resend"]

SCENARIO_SIMULATION_ATTEMPT_ADAPTER_VALUES: set[ScenarioSimulationAttemptAdapter] = {
    "http",
    "resend",
}


def check_scenario_simulation_attempt_adapter(value: str) -> ScenarioSimulationAttemptAdapter:
    if value in SCENARIO_SIMULATION_ATTEMPT_ADAPTER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SIMULATION_ATTEMPT_ADAPTER_VALUES!r}")
