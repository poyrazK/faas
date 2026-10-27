from typing import Literal

EventReplayBackfillJobResponseState = Literal["completed", "completed_with_failures", "running"]

EVENT_REPLAY_BACKFILL_JOB_RESPONSE_STATE_VALUES: set[EventReplayBackfillJobResponseState] = {
    "completed",
    "completed_with_failures",
    "running",
}


def check_event_replay_backfill_job_response_state(value: str) -> EventReplayBackfillJobResponseState:
    if value in EVENT_REPLAY_BACKFILL_JOB_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_REPLAY_BACKFILL_JOB_RESPONSE_STATE_VALUES!r}")
