from typing import Literal

AuditEventResponseSeverity = Literal["high", "info", "warn"]

AUDIT_EVENT_RESPONSE_SEVERITY_VALUES: set[AuditEventResponseSeverity] = {
    "high",
    "info",
    "warn",
}


def check_audit_event_response_severity(value: str) -> AuditEventResponseSeverity:
    if value in AUDIT_EVENT_RESPONSE_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUDIT_EVENT_RESPONSE_SEVERITY_VALUES!r}")
