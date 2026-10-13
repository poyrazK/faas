from typing import Literal

BillingRunExport = Literal["none", "stripe"]

BILLING_RUN_EXPORT_VALUES: set[BillingRunExport] = {
    "none",
    "stripe",
}


def check_billing_run_export(value: str) -> BillingRunExport:
    if value in BILLING_RUN_EXPORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_EXPORT_VALUES!r}")
