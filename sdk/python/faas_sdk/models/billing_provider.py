from typing import Literal

BillingProvider = Literal["lago", "paddle", "stripe"]

BILLING_PROVIDER_VALUES: set[BillingProvider] = {
    "lago",
    "paddle",
    "stripe",
}


def check_billing_provider(value: str) -> BillingProvider:
    if value in BILLING_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_PROVIDER_VALUES!r}")
