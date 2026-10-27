from typing import Literal

EventReceiptRecipientResponseExecutionUnavailable = Literal["cancel_pending", "not_enqueued", "record_unavailable"]

EVENT_RECEIPT_RECIPIENT_RESPONSE_EXECUTION_UNAVAILABLE_VALUES: set[
    EventReceiptRecipientResponseExecutionUnavailable
] = {
    "cancel_pending",
    "not_enqueued",
    "record_unavailable",
}


def check_event_receipt_recipient_response_execution_unavailable(
    value: str,
) -> EventReceiptRecipientResponseExecutionUnavailable:
    if value in EVENT_RECEIPT_RECIPIENT_RESPONSE_EXECUTION_UNAVAILABLE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_RECIPIENT_RESPONSE_EXECUTION_UNAVAILABLE_VALUES!r}"
    )
