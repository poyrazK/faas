from typing import Literal

ScenarioObjectsMutationStatus = Literal[200, 204]

SCENARIO_OBJECTS_MUTATION_STATUS_VALUES: set[ScenarioObjectsMutationStatus] = {
    200,
    204,
}


def check_scenario_objects_mutation_status(value: int) -> ScenarioObjectsMutationStatus:
    if value in SCENARIO_OBJECTS_MUTATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_OBJECTS_MUTATION_STATUS_VALUES!r}")
