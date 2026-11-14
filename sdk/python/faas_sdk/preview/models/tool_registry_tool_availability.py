from typing import Literal

ToolRegistryToolAvailability = Literal["not_checked"]

TOOL_REGISTRY_TOOL_AVAILABILITY_VALUES: set[ToolRegistryToolAvailability] = {
    "not_checked",
}


def check_tool_registry_tool_availability(value: str) -> ToolRegistryToolAvailability:
    if value in TOOL_REGISTRY_TOOL_AVAILABILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_REGISTRY_TOOL_AVAILABILITY_VALUES!r}")
