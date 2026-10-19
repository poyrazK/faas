from typing import Literal

StatementInvoiceExportResponsePaymentStatus = Literal["deleted", "draft", "open", "paid", "uncollectible", "void"]

STATEMENT_INVOICE_EXPORT_RESPONSE_PAYMENT_STATUS_VALUES: set[StatementInvoiceExportResponsePaymentStatus] = {
    "deleted",
    "draft",
    "open",
    "paid",
    "uncollectible",
    "void",
}


def check_statement_invoice_export_response_payment_status(value: str) -> StatementInvoiceExportResponsePaymentStatus:
    if value in STATEMENT_INVOICE_EXPORT_RESPONSE_PAYMENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STATEMENT_INVOICE_EXPORT_RESPONSE_PAYMENT_STATUS_VALUES!r}"
    )
