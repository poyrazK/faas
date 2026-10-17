from typing import Literal

BillingRunResponseStatus = Literal["completed", "queued", "running"]

BILLING_RUN_RESPONSE_STATUS_VALUES: set[BillingRunResponseStatus] = {
    "completed",
    "queued",
    "running",
}


def check_billing_run_response_status(value: str) -> BillingRunResponseStatus:
    if value in BILLING_RUN_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_RESPONSE_STATUS_VALUES!r}")
