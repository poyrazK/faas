from typing import Literal

ExportFOCUSInvoicesFormat = Literal["csv", "metadata", "zip"]

EXPORT_FOCUS_INVOICES_FORMAT_VALUES: set[ExportFOCUSInvoicesFormat] = {
    "csv",
    "metadata",
    "zip",
}


def check_export_focus_invoices_format(value: str) -> ExportFOCUSInvoicesFormat:
    if value in EXPORT_FOCUS_INVOICES_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXPORT_FOCUS_INVOICES_FORMAT_VALUES!r}")
