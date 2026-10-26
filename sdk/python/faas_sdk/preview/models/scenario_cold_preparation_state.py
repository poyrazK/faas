from typing import Literal

ScenarioColdPreparationState = Literal["armed", "booted", "claimed", "failed", "fenced", "ready", "retired"]

SCENARIO_COLD_PREPARATION_STATE_VALUES: set[ScenarioColdPreparationState] = {
    "armed",
    "booted",
    "claimed",
    "failed",
    "fenced",
    "ready",
    "retired",
}


def check_scenario_cold_preparation_state(value: str) -> ScenarioColdPreparationState:
    if value in SCENARIO_COLD_PREPARATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_COLD_PREPARATION_STATE_VALUES!r}")
