from typing import Literal

ListEventReplayBackfillItemsState = Literal[
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

LIST_EVENT_REPLAY_BACKFILL_ITEMS_STATE_VALUES: set[ListEventReplayBackfillItemsState] = {
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


def check_list_event_replay_backfill_items_state(value: str) -> ListEventReplayBackfillItemsState:
    if value in LIST_EVENT_REPLAY_BACKFILL_ITEMS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EVENT_REPLAY_BACKFILL_ITEMS_STATE_VALUES!r}")
