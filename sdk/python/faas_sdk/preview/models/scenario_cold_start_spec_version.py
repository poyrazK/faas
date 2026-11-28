from typing import Literal

ScenarioColdStartSpecVersion = Literal[1]

SCENARIO_COLD_START_SPEC_VERSION_VALUES: set[ScenarioColdStartSpecVersion] = {
    1,
}


def check_scenario_cold_start_spec_version(value: int) -> ScenarioColdStartSpecVersion:
    if value in SCENARIO_COLD_START_SPEC_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_COLD_START_SPEC_VERSION_VALUES!r}")
