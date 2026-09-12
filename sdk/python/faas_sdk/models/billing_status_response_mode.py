from typing import Literal

BillingStatusResponseMode = Literal["disabled", "live"]

BILLING_STATUS_RESPONSE_MODE_VALUES: set[BillingStatusResponseMode] = {
    "disabled",
    "live",
}


def check_billing_status_response_mode(value: str) -> BillingStatusResponseMode:
    if value in BILLING_STATUS_RESPONSE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_STATUS_RESPONSE_MODE_VALUES!r}")
