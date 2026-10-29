from typing import Literal

ScenarioObjectsMutationMethod = Literal["DELETE", "PUT"]

SCENARIO_OBJECTS_MUTATION_METHOD_VALUES: set[ScenarioObjectsMutationMethod] = {
    "DELETE",
    "PUT",
}


def check_scenario_objects_mutation_method(value: str) -> ScenarioObjectsMutationMethod:
    if value in SCENARIO_OBJECTS_MUTATION_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_OBJECTS_MUTATION_METHOD_VALUES!r}")
