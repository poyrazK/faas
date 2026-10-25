from typing import Literal

GdprAuditExportResponseAction = Literal["delete", "export", "restore"]

GDPR_AUDIT_EXPORT_RESPONSE_ACTION_VALUES: set[GdprAuditExportResponseAction] = {
    "delete",
    "export",
    "restore",
}


def check_gdpr_audit_export_response_action(value: str) -> GdprAuditExportResponseAction:
    if value in GDPR_AUDIT_EXPORT_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GDPR_AUDIT_EXPORT_RESPONSE_ACTION_VALUES!r}")
