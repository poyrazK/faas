from typing import Literal

ToolRegistryToolAccessMode = Literal["external-oauth", "open"]

TOOL_REGISTRY_TOOL_ACCESS_MODE_VALUES: set[ToolRegistryToolAccessMode] = {
    "external-oauth",
    "open",
}


def check_tool_registry_tool_access_mode(value: str) -> ToolRegistryToolAccessMode:
    if value in TOOL_REGISTRY_TOOL_ACCESS_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_REGISTRY_TOOL_ACCESS_MODE_VALUES!r}")
