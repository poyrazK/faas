from typing import Literal

EventReceiptRecipientResponseOrigin = Literal["acceptance", "backfill"]

EVENT_RECEIPT_RECIPIENT_RESPONSE_ORIGIN_VALUES: set[EventReceiptRecipientResponseOrigin] = {
    "acceptance",
    "backfill",
}


def check_event_receipt_recipient_response_origin(value: str) -> EventReceiptRecipientResponseOrigin:
    if value in EVENT_RECEIPT_RECIPIENT_RESPONSE_ORIGIN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_RECIPIENT_RESPONSE_ORIGIN_VALUES!r}")
