from typing import Literal

ToolRegistryVersionAvailability = Literal["not_checked"]

TOOL_REGISTRY_VERSION_AVAILABILITY_VALUES: set[ToolRegistryVersionAvailability] = {
    "not_checked",
}


def check_tool_registry_version_availability(value: str) -> ToolRegistryVersionAvailability:
    if value in TOOL_REGISTRY_VERSION_AVAILABILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_REGISTRY_VERSION_AVAILABILITY_VALUES!r}")
