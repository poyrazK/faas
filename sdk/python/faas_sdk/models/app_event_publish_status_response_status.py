from typing import Literal

AppEventPublishStatusResponseStatus = Literal["accepted", "processing", "unavailable"]

APP_EVENT_PUBLISH_STATUS_RESPONSE_STATUS_VALUES: set[AppEventPublishStatusResponseStatus] = {
    "accepted",
    "processing",
    "unavailable",
}


def check_app_event_publish_status_response_status(value: str) -> AppEventPublishStatusResponseStatus:
    if value in APP_EVENT_PUBLISH_STATUS_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_EVENT_PUBLISH_STATUS_RESPONSE_STATUS_VALUES!r}")
