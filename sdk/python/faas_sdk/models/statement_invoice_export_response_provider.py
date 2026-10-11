from typing import Literal

StatementInvoiceExportResponseProvider = Literal["stripe"]

STATEMENT_INVOICE_EXPORT_RESPONSE_PROVIDER_VALUES: set[StatementInvoiceExportResponseProvider] = {
    "stripe",
}


def check_statement_invoice_export_response_provider(value: str) -> StatementInvoiceExportResponseProvider:
    if value in STATEMENT_INVOICE_EXPORT_RESPONSE_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STATEMENT_INVOICE_EXPORT_RESPONSE_PROVIDER_VALUES!r}"
    )
