from typing import Literal

GlobalRuntimeConsentResponseStatus = Literal["active", "expired", "pending", "revoked", "revoking"]

GLOBAL_RUNTIME_CONSENT_RESPONSE_STATUS_VALUES: set[GlobalRuntimeConsentResponseStatus] = {
    "active",
    "expired",
    "pending",
    "revoked",
    "revoking",
}


def check_global_runtime_consent_response_status(value: str) -> GlobalRuntimeConsentResponseStatus:
    if value in GLOBAL_RUNTIME_CONSENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GLOBAL_RUNTIME_CONSENT_RESPONSE_STATUS_VALUES!r}")
