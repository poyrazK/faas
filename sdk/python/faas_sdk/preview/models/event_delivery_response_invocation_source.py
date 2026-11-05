from typing import Literal

EventDeliveryResponseInvocationSource = Literal["async_invoke", "replay"]

EVENT_DELIVERY_RESPONSE_INVOCATION_SOURCE_VALUES: set[EventDeliveryResponseInvocationSource] = {
    "async_invoke",
    "replay",
}


def check_event_delivery_response_invocation_source(value: str) -> EventDeliveryResponseInvocationSource:
    if value in EVENT_DELIVERY_RESPONSE_INVOCATION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_DELIVERY_RESPONSE_INVOCATION_SOURCE_VALUES!r}")
