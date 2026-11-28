from typing import Literal

EventReplayBackfillJobResponseDuplicatePolicy = Literal["skip_existing"]

EVENT_REPLAY_BACKFILL_JOB_RESPONSE_DUPLICATE_POLICY_VALUES: set[EventReplayBackfillJobResponseDuplicatePolicy] = {
    "skip_existing",
}


def check_event_replay_backfill_job_response_duplicate_policy(
    value: str,
) -> EventReplayBackfillJobResponseDuplicatePolicy:
    if value in EVENT_REPLAY_BACKFILL_JOB_RESPONSE_DUPLICATE_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_REPLAY_BACKFILL_JOB_RESPONSE_DUPLICATE_POLICY_VALUES!r}"
    )
