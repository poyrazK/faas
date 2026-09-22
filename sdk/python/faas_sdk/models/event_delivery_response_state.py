from typing import Literal

EventDeliveryResponseState = Literal["cancelled", "completed", "dead_letter", "dispatching", "failed", "pending"]

EVENT_DELIVERY_RESPONSE_STATE_VALUES: set[EventDeliveryResponseState] = {
    "cancelled",
    "completed",
    "dead_letter",
    "dispatching",
    "failed",
    "pending",
}


def check_event_delivery_response_state(value: str) -> EventDeliveryResponseState:
    if value in EVENT_DELIVERY_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_DELIVERY_RESPONSE_STATE_VALUES!r}")
