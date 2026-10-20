from typing import Literal

EnvironmentDefinitionQueuePruningPolicy = Literal["retain"]

ENVIRONMENT_DEFINITION_QUEUE_PRUNING_POLICY_VALUES: set[EnvironmentDefinitionQueuePruningPolicy] = {
    "retain",
}


def check_environment_definition_queue_pruning_policy(value: str) -> EnvironmentDefinitionQueuePruningPolicy:
    if value in ENVIRONMENT_DEFINITION_QUEUE_PRUNING_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_DEFINITION_QUEUE_PRUNING_POLICY_VALUES!r}"
    )
