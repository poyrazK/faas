from typing import Literal

AppSecurityFindingSeverity = Literal["critical", "high", "info", "low", "medium"]

APP_SECURITY_FINDING_SEVERITY_VALUES: set[AppSecurityFindingSeverity] = {
    "critical",
    "high",
    "info",
    "low",
    "medium",
}


def check_app_security_finding_severity(value: str) -> AppSecurityFindingSeverity:
    if value in APP_SECURITY_FINDING_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SECURITY_FINDING_SEVERITY_VALUES!r}")
