from typing import Literal

BillingConnectionResponseCollection = Literal["charge", "none", "send"]

BILLING_CONNECTION_RESPONSE_COLLECTION_VALUES: set[BillingConnectionResponseCollection] = {
    "charge",
    "none",
    "send",
}


def check_billing_connection_response_collection(value: str) -> BillingConnectionResponseCollection:
    if value in BILLING_CONNECTION_RESPONSE_COLLECTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_CONNECTION_RESPONSE_COLLECTION_VALUES!r}")
