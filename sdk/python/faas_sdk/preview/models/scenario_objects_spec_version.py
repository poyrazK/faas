from typing import Literal

ScenarioObjectsSpecVersion = Literal[1]

SCENARIO_OBJECTS_SPEC_VERSION_VALUES: set[ScenarioObjectsSpecVersion] = {
    1,
}


def check_scenario_objects_spec_version(value: int) -> ScenarioObjectsSpecVersion:
    if value in SCENARIO_OBJECTS_SPEC_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_OBJECTS_SPEC_VERSION_VALUES!r}")
