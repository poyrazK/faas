from typing import Literal

PutBillingConnectionRequestCollection = Literal["charge", "none", "send"]

PUT_BILLING_CONNECTION_REQUEST_COLLECTION_VALUES: set[PutBillingConnectionRequestCollection] = {
    "charge",
    "none",
    "send",
}


def check_put_billing_connection_request_collection(value: str) -> PutBillingConnectionRequestCollection:
    if value in PUT_BILLING_CONNECTION_REQUEST_COLLECTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUT_BILLING_CONNECTION_REQUEST_COLLECTION_VALUES!r}")
