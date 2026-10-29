from typing import Literal

EventRecipientBlockerBatchItemError = Literal["read_error", "read_timeout"]

EVENT_RECIPIENT_BLOCKER_BATCH_ITEM_ERROR_VALUES: set[EventRecipientBlockerBatchItemError] = {
    "read_error",
    "read_timeout",
}


def check_event_recipient_blocker_batch_item_error(value: str) -> EventRecipientBlockerBatchItemError:
    if value in EVENT_RECIPIENT_BLOCKER_BATCH_ITEM_ERROR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECIPIENT_BLOCKER_BATCH_ITEM_ERROR_VALUES!r}")
