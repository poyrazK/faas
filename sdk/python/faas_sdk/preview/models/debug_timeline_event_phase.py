from typing import Literal

DebugTimelineEventPhase = Literal["error", "guest", "regression", "request", "wake"]

DEBUG_TIMELINE_EVENT_PHASE_VALUES: set[DebugTimelineEventPhase] = {
    "error",
    "guest",
    "regression",
    "request",
    "wake",
}


def check_debug_timeline_event_phase(value: str) -> DebugTimelineEventPhase:
    if value in DEBUG_TIMELINE_EVENT_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_TIMELINE_EVENT_PHASE_VALUES!r}")
