from typing import Literal

ReleaseTimelineEventKind = Literal[
    "aborted",
    "advanced",
    "approval_expired",
    "approval_requested",
    "approved",
    "completed",
    "created",
    "held",
    "note",
    "rejected",
    "rollback_requested",
    "rolled_back",
    "rollout_started",
    "schedule_blocked",
    "schedule_cancelled",
    "schedule_started",
    "scheduled",
    "superseded",
    "traffic_changed",
    "watch_passed",
]

RELEASE_TIMELINE_EVENT_KIND_VALUES: set[ReleaseTimelineEventKind] = {
    "aborted",
    "advanced",
    "approval_expired",
    "approval_requested",
    "approved",
    "completed",
    "created",
    "held",
    "note",
    "rejected",
    "rollback_requested",
    "rolled_back",
    "rollout_started",
    "schedule_blocked",
    "schedule_cancelled",
    "schedule_started",
    "scheduled",
    "superseded",
    "traffic_changed",
    "watch_passed",
}


def check_release_timeline_event_kind(value: str) -> ReleaseTimelineEventKind:
    if value in RELEASE_TIMELINE_EVENT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_TIMELINE_EVENT_KIND_VALUES!r}")
