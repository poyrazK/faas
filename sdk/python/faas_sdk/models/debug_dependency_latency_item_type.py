from typing import Literal

DebugDependencyLatencyItemType = Literal[
    "application", "guest_transport", "managed_binding", "outbound_integration", "platform_internal"
]

DEBUG_DEPENDENCY_LATENCY_ITEM_TYPE_VALUES: set[DebugDependencyLatencyItemType] = {
    "application",
    "guest_transport",
    "managed_binding",
    "outbound_integration",
    "platform_internal",
}


def check_debug_dependency_latency_item_type(value: str) -> DebugDependencyLatencyItemType:
    if value in DEBUG_DEPENDENCY_LATENCY_ITEM_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_DEPENDENCY_LATENCY_ITEM_TYPE_VALUES!r}")
