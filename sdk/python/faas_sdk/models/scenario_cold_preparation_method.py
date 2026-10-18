from typing import Literal

ScenarioColdPreparationMethod = Literal["cold_boot"]

SCENARIO_COLD_PREPARATION_METHOD_VALUES: set[ScenarioColdPreparationMethod] = {
    "cold_boot",
}


def check_scenario_cold_preparation_method(value: str) -> ScenarioColdPreparationMethod:
    if value in SCENARIO_COLD_PREPARATION_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_COLD_PREPARATION_METHOD_VALUES!r}")
