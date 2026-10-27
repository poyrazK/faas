from typing import Literal

AppEventPublicationVerificationStatus = Literal["conflict", "match", "unavailable"]

APP_EVENT_PUBLICATION_VERIFICATION_STATUS_VALUES: set[AppEventPublicationVerificationStatus] = {
    "conflict",
    "match",
    "unavailable",
}


def check_app_event_publication_verification_status(value: str) -> AppEventPublicationVerificationStatus:
    if value in APP_EVENT_PUBLICATION_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_EVENT_PUBLICATION_VERIFICATION_STATUS_VALUES!r}")
