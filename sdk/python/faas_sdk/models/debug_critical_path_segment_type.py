from typing import Literal

DebugCriticalPathSegmentType = Literal[
    "application", "guest_transport", "managed_binding", "outbound_integration", "platform_internal"
]

DEBUG_CRITICAL_PATH_SEGMENT_TYPE_VALUES: set[DebugCriticalPathSegmentType] = {
    "application",
    "guest_transport",
    "managed_binding",
    "outbound_integration",
    "platform_internal",
}


def check_debug_critical_path_segment_type(value: str) -> DebugCriticalPathSegmentType:
    if value in DEBUG_CRITICAL_PATH_SEGMENT_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_CRITICAL_PATH_SEGMENT_TYPE_VALUES!r}")
