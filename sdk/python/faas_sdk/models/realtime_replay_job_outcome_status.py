from typing import Literal

RealtimeReplayJobOutcomeStatus = Literal[
    "deadline_expired", "eligible", "invalid_deadline", "missing", "not_dead", "ordering_blocked", "replayed", "stale"
]

REALTIME_REPLAY_JOB_OUTCOME_STATUS_VALUES: set[RealtimeReplayJobOutcomeStatus] = {
    "deadline_expired",
    "eligible",
    "invalid_deadline",
    "missing",
    "not_dead",
    "ordering_blocked",
    "replayed",
    "stale",
}


def check_realtime_replay_job_outcome_status(value: str) -> RealtimeReplayJobOutcomeStatus:
    if value in REALTIME_REPLAY_JOB_OUTCOME_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REALTIME_REPLAY_JOB_OUTCOME_STATUS_VALUES!r}")
