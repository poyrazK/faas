from typing import Literal

EventReceiptRoutingResponseCapacityScope = Literal["account", "app", "consumer"]

EVENT_RECEIPT_ROUTING_RESPONSE_CAPACITY_SCOPE_VALUES: set[EventReceiptRoutingResponseCapacityScope] = {
    "account",
    "app",
    "consumer",
}


def check_event_receipt_routing_response_capacity_scope(value: str) -> EventReceiptRoutingResponseCapacityScope:
    if value in EVENT_RECEIPT_ROUTING_RESPONSE_CAPACITY_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_ROUTING_RESPONSE_CAPACITY_SCOPE_VALUES!r}"
    )
