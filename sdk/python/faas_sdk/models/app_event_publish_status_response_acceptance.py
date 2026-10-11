from typing import Literal

AppEventPublishStatusResponseAcceptance = Literal["replacement_acceptance", "same_acceptance", "unavailable"]

APP_EVENT_PUBLISH_STATUS_RESPONSE_ACCEPTANCE_VALUES: set[AppEventPublishStatusResponseAcceptance] = {
    "replacement_acceptance",
    "same_acceptance",
    "unavailable",
}


def check_app_event_publish_status_response_acceptance(value: str) -> AppEventPublishStatusResponseAcceptance:
    if value in APP_EVENT_PUBLISH_STATUS_RESPONSE_ACCEPTANCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_EVENT_PUBLISH_STATUS_RESPONSE_ACCEPTANCE_VALUES!r}"
    )
