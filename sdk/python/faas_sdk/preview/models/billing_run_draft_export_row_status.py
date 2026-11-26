from typing import Literal

BillingRunDraftExportRowStatus = Literal["blocked", "exported", "ready"]

BILLING_RUN_DRAFT_EXPORT_ROW_STATUS_VALUES: set[BillingRunDraftExportRowStatus] = {
    "blocked",
    "exported",
    "ready",
}


def check_billing_run_draft_export_row_status(value: str) -> BillingRunDraftExportRowStatus:
    if value in BILLING_RUN_DRAFT_EXPORT_ROW_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_DRAFT_EXPORT_ROW_STATUS_VALUES!r}")
