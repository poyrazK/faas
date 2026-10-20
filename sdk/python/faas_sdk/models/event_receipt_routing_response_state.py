from typing import Literal

EventReceiptRoutingResponseState = Literal["enqueued", "failed", "filtered", "pending", "processing"]

EVENT_RECEIPT_ROUTING_RESPONSE_STATE_VALUES: set[EventReceiptRoutingResponseState] = {
    "enqueued",
    "failed",
    "filtered",
    "pending",
    "processing",
}


def check_event_receipt_routing_response_state(value: str) -> EventReceiptRoutingResponseState:
    if value in EVENT_RECEIPT_ROUTING_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_ROUTING_RESPONSE_STATE_VALUES!r}")
