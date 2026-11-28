from typing import Literal

AppSecurityQuarantineReason = Literal["security_scan_regressed"]

APP_SECURITY_QUARANTINE_REASON_VALUES: set[AppSecurityQuarantineReason] = {
    "security_scan_regressed",
}


def check_app_security_quarantine_reason(value: str) -> AppSecurityQuarantineReason:
    if value in APP_SECURITY_QUARANTINE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SECURITY_QUARANTINE_REASON_VALUES!r}")
