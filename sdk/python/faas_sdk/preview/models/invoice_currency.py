from typing import Literal

InvoiceCurrency = Literal["eur"]

INVOICE_CURRENCY_VALUES: set[InvoiceCurrency] = {
    "eur",
}


def check_invoice_currency(value: str) -> InvoiceCurrency:
    if value in INVOICE_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVOICE_CURRENCY_VALUES!r}")
