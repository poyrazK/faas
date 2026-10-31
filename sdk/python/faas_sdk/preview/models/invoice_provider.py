from typing import Literal

InvoiceProvider = Literal["paddle", "stripe"]

INVOICE_PROVIDER_VALUES: set[InvoiceProvider] = {
    "paddle",
    "stripe",
}


def check_invoice_provider(value: str) -> InvoiceProvider:
    if value in INVOICE_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVOICE_PROVIDER_VALUES!r}")
