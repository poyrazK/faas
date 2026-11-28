from typing import Literal

RealtimeDeadLetterReplayOutcomeStatus = Literal[
    "deadline_expired", "invalid_deadline", "missing", "not_dead", "ordering_blocked", "replayed", "stale"
]

REALTIME_DEAD_LETTER_REPLAY_OUTCOME_STATUS_VALUES: set[RealtimeDeadLetterReplayOutcomeStatus] = {
    "deadline_expired",
    "invalid_deadline",
    "missing",
    "not_dead",
    "ordering_blocked",
    "replayed",
    "stale",
}


def check_realtime_dead_letter_replay_outcome_status(value: str) -> RealtimeDeadLetterReplayOutcomeStatus:
    if value in REALTIME_DEAD_LETTER_REPLAY_OUTCOME_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_DEAD_LETTER_REPLAY_OUTCOME_STATUS_VALUES!r}"
    )
