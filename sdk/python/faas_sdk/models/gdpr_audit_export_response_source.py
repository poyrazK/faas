from typing import Literal

GdprAuditExportResponseSource = Literal["event", "gdpr"]

GDPR_AUDIT_EXPORT_RESPONSE_SOURCE_VALUES: set[GdprAuditExportResponseSource] = {
    "event",
    "gdpr",
}


def check_gdpr_audit_export_response_source(value: str) -> GdprAuditExportResponseSource:
    if value in GDPR_AUDIT_EXPORT_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GDPR_AUDIT_EXPORT_RESPONSE_SOURCE_VALUES!r}")
