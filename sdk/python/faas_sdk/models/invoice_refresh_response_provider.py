from typing import Literal

InvoiceRefreshResponseProvider = Literal["paddle", "polar", "stripe"]

INVOICE_REFRESH_RESPONSE_PROVIDER_VALUES: set[InvoiceRefreshResponseProvider] = {
    "paddle",
    "polar",
    "stripe",
}


def check_invoice_refresh_response_provider(value: str) -> InvoiceRefreshResponseProvider:
    if value in INVOICE_REFRESH_RESPONSE_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVOICE_REFRESH_RESPONSE_PROVIDER_VALUES!r}")
