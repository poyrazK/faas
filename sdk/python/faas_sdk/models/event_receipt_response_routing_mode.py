from typing import Literal

EventReceiptResponseRoutingMode = Literal["event", "recipient"]

EVENT_RECEIPT_RESPONSE_ROUTING_MODE_VALUES: set[EventReceiptResponseRoutingMode] = {
    "event",
    "recipient",
}


def check_event_receipt_response_routing_mode(value: str) -> EventReceiptResponseRoutingMode:
    if value in EVENT_RECEIPT_RESPONSE_ROUTING_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_RESPONSE_ROUTING_MODE_VALUES!r}")
