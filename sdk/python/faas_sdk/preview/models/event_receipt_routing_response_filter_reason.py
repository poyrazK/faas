from typing import Literal

EventReceiptRoutingResponseFilterReason = Literal["schema_version_mismatch"]

EVENT_RECEIPT_ROUTING_RESPONSE_FILTER_REASON_VALUES: set[EventReceiptRoutingResponseFilterReason] = {
    "schema_version_mismatch",
}


def check_event_receipt_routing_response_filter_reason(value: str) -> EventReceiptRoutingResponseFilterReason:
    if value in EVENT_RECEIPT_ROUTING_RESPONSE_FILTER_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_ROUTING_RESPONSE_FILTER_REASON_VALUES!r}"
    )
