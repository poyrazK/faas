from typing import Literal

BillingStatusResponsePlan = Literal["free", "hobby", "pro", "scale"]

BILLING_STATUS_RESPONSE_PLAN_VALUES: set[BillingStatusResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_billing_status_response_plan(value: str) -> BillingStatusResponsePlan:
    if value in BILLING_STATUS_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_STATUS_RESPONSE_PLAN_VALUES!r}")
