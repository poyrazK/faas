from typing import Literal

AppEventPublicationVerificationReason = Literal["not_retained_or_not_observed"]

APP_EVENT_PUBLICATION_VERIFICATION_REASON_VALUES: set[AppEventPublicationVerificationReason] = {
    "not_retained_or_not_observed",
}


def check_app_event_publication_verification_reason(value: str) -> AppEventPublicationVerificationReason:
    if value in APP_EVENT_PUBLICATION_VERIFICATION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_EVENT_PUBLICATION_VERIFICATION_REASON_VALUES!r}")
