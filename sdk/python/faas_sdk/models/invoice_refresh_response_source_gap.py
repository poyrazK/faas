from typing import Literal

InvoiceRefreshResponseSourceGap = Literal[
    "empty", "incomplete", "tax_in_non_tax_lines", "totals_mismatch", "unavailable", "unclassified"
]

INVOICE_REFRESH_RESPONSE_SOURCE_GAP_VALUES: set[InvoiceRefreshResponseSourceGap] = {
    "empty",
    "incomplete",
    "tax_in_non_tax_lines",
    "totals_mismatch",
    "unavailable",
    "unclassified",
}


def check_invoice_refresh_response_source_gap(value: str) -> InvoiceRefreshResponseSourceGap:
    if value in INVOICE_REFRESH_RESPONSE_SOURCE_GAP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVOICE_REFRESH_RESPONSE_SOURCE_GAP_VALUES!r}")
