from typing import Literal

EventReceiptRoutingResponseRetryStopReason = Literal[
    "delivery_expired", "max_attempts", "max_duration", "non_retryable"
]

EVENT_RECEIPT_ROUTING_RESPONSE_RETRY_STOP_REASON_VALUES: set[EventReceiptRoutingResponseRetryStopReason] = {
    "delivery_expired",
    "max_attempts",
    "max_duration",
    "non_retryable",
}


def check_event_receipt_routing_response_retry_stop_reason(value: str) -> EventReceiptRoutingResponseRetryStopReason:
    if value in EVENT_RECEIPT_ROUTING_RESPONSE_RETRY_STOP_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_ROUTING_RESPONSE_RETRY_STOP_REASON_VALUES!r}"
    )
