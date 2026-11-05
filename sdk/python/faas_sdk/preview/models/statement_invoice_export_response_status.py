from typing import Literal

StatementInvoiceExportResponseStatus = Literal["completed", "creating"]

STATEMENT_INVOICE_EXPORT_RESPONSE_STATUS_VALUES: set[StatementInvoiceExportResponseStatus] = {
    "completed",
    "creating",
}


def check_statement_invoice_export_response_status(value: str) -> StatementInvoiceExportResponseStatus:
    if value in STATEMENT_INVOICE_EXPORT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STATEMENT_INVOICE_EXPORT_RESPONSE_STATUS_VALUES!r}")
