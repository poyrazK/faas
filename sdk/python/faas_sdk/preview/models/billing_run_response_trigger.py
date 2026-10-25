from typing import Literal

BillingRunResponseTrigger = Literal["api", "schedule"]

BILLING_RUN_RESPONSE_TRIGGER_VALUES: set[BillingRunResponseTrigger] = {
    "api",
    "schedule",
}


def check_billing_run_response_trigger(value: str) -> BillingRunResponseTrigger:
    if value in BILLING_RUN_RESPONSE_TRIGGER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_RESPONSE_TRIGGER_VALUES!r}")
