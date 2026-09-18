from typing import Literal

DebugTelemetrySpanDependencyType = Literal[
    "guest_transport", "managed_binding", "outbound_integration", "platform_internal"
]

DEBUG_TELEMETRY_SPAN_DEPENDENCY_TYPE_VALUES: set[DebugTelemetrySpanDependencyType] = {
    "guest_transport",
    "managed_binding",
    "outbound_integration",
    "platform_internal",
}


def check_debug_telemetry_span_dependency_type(value: str) -> DebugTelemetrySpanDependencyType:
    if value in DEBUG_TELEMETRY_SPAN_DEPENDENCY_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_TELEMETRY_SPAN_DEPENDENCY_TYPE_VALUES!r}")
