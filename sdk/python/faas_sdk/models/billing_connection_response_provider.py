from typing import Literal

BillingConnectionResponseProvider = Literal["stripe"]

BILLING_CONNECTION_RESPONSE_PROVIDER_VALUES: set[BillingConnectionResponseProvider] = {
    "stripe",
}


def check_billing_connection_response_provider(value: str) -> BillingConnectionResponseProvider:
    if value in BILLING_CONNECTION_RESPONSE_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_CONNECTION_RESPONSE_PROVIDER_VALUES!r}")
