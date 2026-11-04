from typing import Literal

SidecarTimelineStatusStatus = Literal["failed", "healthy", "ready", "restarting", "starting", "unhealthy", "unready"]

SIDECAR_TIMELINE_STATUS_STATUS_VALUES: set[SidecarTimelineStatusStatus] = {
    "failed",
    "healthy",
    "ready",
    "restarting",
    "starting",
    "unhealthy",
    "unready",
}


def check_sidecar_timeline_status_status(value: str) -> SidecarTimelineStatusStatus:
    if value in SIDECAR_TIMELINE_STATUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SIDECAR_TIMELINE_STATUS_STATUS_VALUES!r}")
