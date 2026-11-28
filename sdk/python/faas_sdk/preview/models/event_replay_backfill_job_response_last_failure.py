from typing import Literal

EventReplayBackfillJobResponseLastFailure = Literal["conflict", "internal", "invalid_job", "timeout"]

EVENT_REPLAY_BACKFILL_JOB_RESPONSE_LAST_FAILURE_VALUES: set[EventReplayBackfillJobResponseLastFailure] = {
    "conflict",
    "internal",
    "invalid_job",
    "timeout",
}


def check_event_replay_backfill_job_response_last_failure(value: str) -> EventReplayBackfillJobResponseLastFailure:
    if value in EVENT_REPLAY_BACKFILL_JOB_RESPONSE_LAST_FAILURE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_REPLAY_BACKFILL_JOB_RESPONSE_LAST_FAILURE_VALUES!r}"
    )
