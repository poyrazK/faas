from typing import Literal

DeliverAppEventResponseStatus = Literal["pending"]

DELIVER_APP_EVENT_RESPONSE_STATUS_VALUES: set[DeliverAppEventResponseStatus] = {
    "pending",
}


def check_deliver_app_event_response_status(value: str) -> DeliverAppEventResponseStatus:
    if value in DELIVER_APP_EVENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DELIVER_APP_EVENT_RESPONSE_STATUS_VALUES!r}")
