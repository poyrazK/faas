from typing import Literal

AppSecretRevocationResponseStatus = Literal["blocked", "complete", "failed", "pending"]

APP_SECRET_REVOCATION_RESPONSE_STATUS_VALUES: set[AppSecretRevocationResponseStatus] = {
    "blocked",
    "complete",
    "failed",
    "pending",
}


def check_app_secret_revocation_response_status(value: str) -> AppSecretRevocationResponseStatus:
    if value in APP_SECRET_REVOCATION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SECRET_REVOCATION_RESPONSE_STATUS_VALUES!r}")
