from typing import Literal

ApplicationStandardEnrollmentState = Literal["applying", "blocked", "observed", "pending", "persisted", "unmanaged"]

APPLICATION_STANDARD_ENROLLMENT_STATE_VALUES: set[ApplicationStandardEnrollmentState] = {
    "applying",
    "blocked",
    "observed",
    "pending",
    "persisted",
    "unmanaged",
}


def check_application_standard_enrollment_state(value: str) -> ApplicationStandardEnrollmentState:
    if value in APPLICATION_STANDARD_ENROLLMENT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_ENROLLMENT_STATE_VALUES!r}")
