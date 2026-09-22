from typing import Literal

SecurityQuarantineRecoveryResponseStatus = Literal["active"]

SECURITY_QUARANTINE_RECOVERY_RESPONSE_STATUS_VALUES: set[SecurityQuarantineRecoveryResponseStatus] = {
    "active",
}


def check_security_quarantine_recovery_response_status(value: str) -> SecurityQuarantineRecoveryResponseStatus:
    if value in SECURITY_QUARANTINE_RECOVERY_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SECURITY_QUARANTINE_RECOVERY_RESPONSE_STATUS_VALUES!r}"
    )
