from typing import Literal

StatementInvoiceExportResponseCollection = Literal["charge", "none", "send"]

STATEMENT_INVOICE_EXPORT_RESPONSE_COLLECTION_VALUES: set[StatementInvoiceExportResponseCollection] = {
    "charge",
    "none",
    "send",
}


def check_statement_invoice_export_response_collection(value: str) -> StatementInvoiceExportResponseCollection:
    if value in STATEMENT_INVOICE_EXPORT_RESPONSE_COLLECTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STATEMENT_INVOICE_EXPORT_RESPONSE_COLLECTION_VALUES!r}"
    )
