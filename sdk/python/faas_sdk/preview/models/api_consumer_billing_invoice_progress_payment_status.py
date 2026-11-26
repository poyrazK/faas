from typing import Literal

APIConsumerBillingInvoiceProgressPaymentStatus = Literal["deleted", "draft", "open", "paid", "uncollectible", "void"]

API_CONSUMER_BILLING_INVOICE_PROGRESS_PAYMENT_STATUS_VALUES: set[APIConsumerBillingInvoiceProgressPaymentStatus] = {
    "deleted",
    "draft",
    "open",
    "paid",
    "uncollectible",
    "void",
}


def check_api_consumer_billing_invoice_progress_payment_status(
    value: str,
) -> APIConsumerBillingInvoiceProgressPaymentStatus:
    if value in API_CONSUMER_BILLING_INVOICE_PROGRESS_PAYMENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {API_CONSUMER_BILLING_INVOICE_PROGRESS_PAYMENT_STATUS_VALUES!r}"
    )
