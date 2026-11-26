from typing import Literal

BillingRunDraftExportRowBlockerCode = Literal["stripe_customer_missing"]

BILLING_RUN_DRAFT_EXPORT_ROW_BLOCKER_CODE_VALUES: set[BillingRunDraftExportRowBlockerCode] = {
    "stripe_customer_missing",
}


def check_billing_run_draft_export_row_blocker_code(value: str) -> BillingRunDraftExportRowBlockerCode:
    if value in BILLING_RUN_DRAFT_EXPORT_ROW_BLOCKER_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_DRAFT_EXPORT_ROW_BLOCKER_CODE_VALUES!r}")
