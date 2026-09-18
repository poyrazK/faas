from typing import Literal

DebugRequestDependencyLatencyType = Literal[
    "application", "guest_transport", "managed_binding", "outbound_integration", "platform_internal"
]

DEBUG_REQUEST_DEPENDENCY_LATENCY_TYPE_VALUES: set[DebugRequestDependencyLatencyType] = {
    "application",
    "guest_transport",
    "managed_binding",
    "outbound_integration",
    "platform_internal",
}


def check_debug_request_dependency_latency_type(value: str) -> DebugRequestDependencyLatencyType:
    if value in DEBUG_REQUEST_DEPENDENCY_LATENCY_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_REQUEST_DEPENDENCY_LATENCY_TYPE_VALUES!r}")
