from typing import Literal

ConsumerCreditPurchaseResponseStatus = Literal["credited", "expired", "pending"]

CONSUMER_CREDIT_PURCHASE_RESPONSE_STATUS_VALUES: set[ConsumerCreditPurchaseResponseStatus] = {
    "credited",
    "expired",
    "pending",
}


def check_consumer_credit_purchase_response_status(value: str) -> ConsumerCreditPurchaseResponseStatus:
    if value in CONSUMER_CREDIT_PURCHASE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONSUMER_CREDIT_PURCHASE_RESPONSE_STATUS_VALUES!r}")
