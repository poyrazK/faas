from typing import Literal

ExecutionCapabilitiesResponseRuntimesItem = Literal["node22", "node24", "python312", "python313"]

EXECUTION_CAPABILITIES_RESPONSE_RUNTIMES_ITEM_VALUES: set[ExecutionCapabilitiesResponseRuntimesItem] = {
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_execution_capabilities_response_runtimes_item(value: str) -> ExecutionCapabilitiesResponseRuntimesItem:
    if value in EXECUTION_CAPABILITIES_RESPONSE_RUNTIMES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_CAPABILITIES_RESPONSE_RUNTIMES_ITEM_VALUES!r}"
    )
