from typing import Literal

DebugCriticalPathSpanDependencyType = Literal[
    "guest_transport", "managed_binding", "outbound_integration", "platform_internal"
]

DEBUG_CRITICAL_PATH_SPAN_DEPENDENCY_TYPE_VALUES: set[DebugCriticalPathSpanDependencyType] = {
    "guest_transport",
    "managed_binding",
    "outbound_integration",
    "platform_internal",
}


def check_debug_critical_path_span_dependency_type(value: str) -> DebugCriticalPathSpanDependencyType:
    if value in DEBUG_CRITICAL_PATH_SPAN_DEPENDENCY_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_CRITICAL_PATH_SPAN_DEPENDENCY_TYPE_VALUES!r}")
