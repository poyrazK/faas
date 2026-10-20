from typing import Literal

AppEventPublicationVerificationAcceptance = Literal["replacement_acceptance", "same_acceptance", "unavailable"]

APP_EVENT_PUBLICATION_VERIFICATION_ACCEPTANCE_VALUES: set[AppEventPublicationVerificationAcceptance] = {
    "replacement_acceptance",
    "same_acceptance",
    "unavailable",
}


def check_app_event_publication_verification_acceptance(value: str) -> AppEventPublicationVerificationAcceptance:
    if value in APP_EVENT_PUBLICATION_VERIFICATION_ACCEPTANCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_EVENT_PUBLICATION_VERIFICATION_ACCEPTANCE_VALUES!r}"
    )
