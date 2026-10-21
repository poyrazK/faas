from typing import Literal

StatementInvoiceExportResponseSourceKind = Literal["app_consumer", "platform_tenant", "product_control"]

STATEMENT_INVOICE_EXPORT_RESPONSE_SOURCE_KIND_VALUES: set[StatementInvoiceExportResponseSourceKind] = {
    "app_consumer",
    "platform_tenant",
    "product_control",
}


def check_statement_invoice_export_response_source_kind(value: str) -> StatementInvoiceExportResponseSourceKind:
    if value in STATEMENT_INVOICE_EXPORT_RESPONSE_SOURCE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STATEMENT_INVOICE_EXPORT_RESPONSE_SOURCE_KIND_VALUES!r}"
    )
