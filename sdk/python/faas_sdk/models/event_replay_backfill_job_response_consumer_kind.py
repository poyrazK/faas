from typing import Literal

EventReplayBackfillJobResponseConsumerKind = Literal["application", "workflow"]

EVENT_REPLAY_BACKFILL_JOB_RESPONSE_CONSUMER_KIND_VALUES: set[EventReplayBackfillJobResponseConsumerKind] = {
    "application",
    "workflow",
}


def check_event_replay_backfill_job_response_consumer_kind(value: str) -> EventReplayBackfillJobResponseConsumerKind:
    if value in EVENT_REPLAY_BACKFILL_JOB_RESPONSE_CONSUMER_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_REPLAY_BACKFILL_JOB_RESPONSE_CONSUMER_KIND_VALUES!r}"
    )
