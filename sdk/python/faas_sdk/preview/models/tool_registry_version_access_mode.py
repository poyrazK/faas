from typing import Literal

ToolRegistryVersionAccessMode = Literal["external-oauth", "open"]

TOOL_REGISTRY_VERSION_ACCESS_MODE_VALUES: set[ToolRegistryVersionAccessMode] = {
    "external-oauth",
    "open",
}


def check_tool_registry_version_access_mode(value: str) -> ToolRegistryVersionAccessMode:
    if value in TOOL_REGISTRY_VERSION_ACCESS_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_REGISTRY_VERSION_ACCESS_MODE_VALUES!r}")
