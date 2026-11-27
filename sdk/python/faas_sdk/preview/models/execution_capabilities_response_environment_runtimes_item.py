from typing import Literal

ExecutionCapabilitiesResponseEnvironmentRuntimesItem = Literal["node22", "node24", "python312", "python313"]

EXECUTION_CAPABILITIES_RESPONSE_ENVIRONMENT_RUNTIMES_ITEM_VALUES: set[
    ExecutionCapabilitiesResponseEnvironmentRuntimesItem
] = {
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_execution_capabilities_response_environment_runtimes_item(
    value: str,
) -> ExecutionCapabilitiesResponseEnvironmentRuntimesItem:
    if value in EXECUTION_CAPABILITIES_RESPONSE_ENVIRONMENT_RUNTIMES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_CAPABILITIES_RESPONSE_ENVIRONMENT_RUNTIMES_ITEM_VALUES!r}"
    )
