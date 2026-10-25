from typing import Literal

EnvironmentDefinitionWorkloadPruningPolicy = Literal["retain"]

ENVIRONMENT_DEFINITION_WORKLOAD_PRUNING_POLICY_VALUES: set[EnvironmentDefinitionWorkloadPruningPolicy] = {
    "retain",
}


def check_environment_definition_workload_pruning_policy(value: str) -> EnvironmentDefinitionWorkloadPruningPolicy:
    if value in ENVIRONMENT_DEFINITION_WORKLOAD_PRUNING_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_DEFINITION_WORKLOAD_PRUNING_POLICY_VALUES!r}"
    )
