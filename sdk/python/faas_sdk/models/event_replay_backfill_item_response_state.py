from typing import Literal

EventReplayBackfillItemResponseState = Literal[
    "enqueued",
    "failed",
    "filtered",
    "pending",
    "processing",
    "skipped_captured",
    "skipped_existing",
    "skipped_unknown",
    "skipped_unsettled",
]

EVENT_REPLAY_BACKFILL_ITEM_RESPONSE_STATE_VALUES: set[EventReplayBackfillItemResponseState] = {
    "enqueued",
    "failed",
    "filtered",
    "pending",
    "processing",
    "skipped_captured",
    "skipped_existing",
    "skipped_unknown",
    "skipped_unsettled",
}


def check_event_replay_backfill_item_response_state(value: str) -> EventReplayBackfillItemResponseState:
    if value in EVENT_REPLAY_BACKFILL_ITEM_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_REPLAY_BACKFILL_ITEM_RESPONSE_STATE_VALUES!r}")
