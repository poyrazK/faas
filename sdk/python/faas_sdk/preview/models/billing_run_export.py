from typing import Literal

BillingRunExport = Literal["lago", "none", "paddle", "stripe"]

BILLING_RUN_EXPORT_VALUES: set[BillingRunExport] = {
    "lago",
    "none",
    "paddle",
    "stripe",
}


def check_billing_run_export(value: str) -> BillingRunExport:
    if value in BILLING_RUN_EXPORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_EXPORT_VALUES!r}")
