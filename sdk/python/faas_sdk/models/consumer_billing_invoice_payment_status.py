from typing import Literal

ConsumerBillingInvoicePaymentStatus = Literal["deleted", "draft", "open", "paid", "uncollectible", "void"]

CONSUMER_BILLING_INVOICE_PAYMENT_STATUS_VALUES: set[ConsumerBillingInvoicePaymentStatus] = {
    "deleted",
    "draft",
    "open",
    "paid",
    "uncollectible",
    "void",
}


def check_consumer_billing_invoice_payment_status(value: str) -> ConsumerBillingInvoicePaymentStatus:
    if value in CONSUMER_BILLING_INVOICE_PAYMENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONSUMER_BILLING_INVOICE_PAYMENT_STATUS_VALUES!r}")
