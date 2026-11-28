from typing import Literal

BillingRunItemResponsePaymentStatus = Literal["deleted", "draft", "open", "paid", "uncollectible", "void"]

BILLING_RUN_ITEM_RESPONSE_PAYMENT_STATUS_VALUES: set[BillingRunItemResponsePaymentStatus] = {
    "deleted",
    "draft",
    "open",
    "paid",
    "uncollectible",
    "void",
}


def check_billing_run_item_response_payment_status(value: str) -> BillingRunItemResponsePaymentStatus:
    if value in BILLING_RUN_ITEM_RESPONSE_PAYMENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_ITEM_RESPONSE_PAYMENT_STATUS_VALUES!r}")
