from typing import Literal

ReleaseTimelinePhase = Literal[
    "awaiting_approval",
    "building",
    "cancelled",
    "failed",
    "held",
    "released",
    "rolled_back",
    "rolling_out",
    "scheduled",
    "superseded",
    "watching",
]

RELEASE_TIMELINE_PHASE_VALUES: set[ReleaseTimelinePhase] = {
    "awaiting_approval",
    "building",
    "cancelled",
    "failed",
    "held",
    "released",
    "rolled_back",
    "rolling_out",
    "scheduled",
    "superseded",
    "watching",
}


def check_release_timeline_phase(value: str) -> ReleaseTimelinePhase:
    if value in RELEASE_TIMELINE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_TIMELINE_PHASE_VALUES!r}")
