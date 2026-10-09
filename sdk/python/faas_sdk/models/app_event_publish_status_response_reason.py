from typing import Literal

AppEventPublishStatusResponseReason = Literal["not_retained_or_not_observed"]

APP_EVENT_PUBLISH_STATUS_RESPONSE_REASON_VALUES: set[AppEventPublishStatusResponseReason] = {
    "not_retained_or_not_observed",
}


def check_app_event_publish_status_response_reason(value: str) -> AppEventPublishStatusResponseReason:
    if value in APP_EVENT_PUBLISH_STATUS_RESPONSE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_EVENT_PUBLISH_STATUS_RESPONSE_REASON_VALUES!r}")
