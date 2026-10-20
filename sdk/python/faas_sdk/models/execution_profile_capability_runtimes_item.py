from typing import Literal

ExecutionProfileCapabilityRuntimesItem = Literal["node22", "node24", "python312", "python313"]

EXECUTION_PROFILE_CAPABILITY_RUNTIMES_ITEM_VALUES: set[ExecutionProfileCapabilityRuntimesItem] = {
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_execution_profile_capability_runtimes_item(value: str) -> ExecutionProfileCapabilityRuntimesItem:
    if value in EXECUTION_PROFILE_CAPABILITY_RUNTIMES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_PROFILE_CAPABILITY_RUNTIMES_ITEM_VALUES!r}"
    )
