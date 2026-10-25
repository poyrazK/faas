from typing import Literal

DebugSuspectedDependencyType = Literal[
    "app_dependency", "guest_transport", "managed_binding", "outbound_integration", "platform_internal"
]

DEBUG_SUSPECTED_DEPENDENCY_TYPE_VALUES: set[DebugSuspectedDependencyType] = {
    "app_dependency",
    "guest_transport",
    "managed_binding",
    "outbound_integration",
    "platform_internal",
}


def check_debug_suspected_dependency_type(value: str) -> DebugSuspectedDependencyType:
    if value in DEBUG_SUSPECTED_DEPENDENCY_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_SUSPECTED_DEPENDENCY_TYPE_VALUES!r}")
