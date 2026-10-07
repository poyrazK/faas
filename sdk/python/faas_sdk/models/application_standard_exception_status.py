from typing import Literal

ApplicationStandardExceptionStatus = Literal["active", "expired", "revoked"]

APPLICATION_STANDARD_EXCEPTION_STATUS_VALUES: set[ApplicationStandardExceptionStatus] = {
    "active",
    "expired",
    "revoked",
}


def check_application_standard_exception_status(value: str) -> ApplicationStandardExceptionStatus:
    if value in APPLICATION_STANDARD_EXCEPTION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_EXCEPTION_STATUS_VALUES!r}")
